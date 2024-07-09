// Copyright 2013 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ssa

// This file implements the CREATE phase of SSA construction.
// See builder.go for explanation.

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"os"
	"sync"

	"golang.org/x/tools/internal/versions"
)

// NewProgram returns a new SSA Program.
//
// mode controls diagnostics and checking during SSA construction.
//
// To construct an SSA program:
//
//   - Call NewProgram to create an empty Program.
//   - Call CreatePackage providing typed syntax for each package
//     you want to build, and call it with types but not
//     syntax for each of those package's direct dependencies.
//   - Call [Package.Build] on each syntax package you wish to build,
//     or [Program.Build] to build all of them.
//
// See the Example tests for simple examples.
func NewProgram(fset *token.FileSet, mode BuilderMode) *Program {
	return &Program{
		Fset:     fset,
		imported: make(map[string]*Package),
		packages: make(map[*types.Package]*Package),
		mode:     mode,
		canon:    newCanonizer(),
		ctxt:     types.NewContext(),
	}
}

// memberFromObject populates package pkg with a member for the
// typechecker object obj.
//
// For objects from Go source code, syntax is the associated syntax
// tree (for funcs and vars only) and goversion defines the
// appropriate interpretation; they will be used during the build
// phase.
func memberFromObject(pkg *Package, obj types.Object, syntax ast.Node, goversion string) {
	name := obj.Name()
	switch obj := obj.(type) {
	case *types.Builtin:
		if pkg.Pkg != types.Unsafe {
			panic("unexpected builtin object: " + obj.String())
		}

	case *types.TypeName:
		if name != "_" {
			pkg.Members[name] = &Type{
				object: obj,
				pkg:    pkg,
			}
		}

	case *types.Const:
		c := &NamedConst{
			object: obj,
			Value:  NewConst(obj.Val(), obj.Type()),
			pkg:    pkg,
		}
		pkg.objects[obj] = c
		if name != "_" {
			pkg.Members[name] = c
		}

	case *types.Var:
		g := &Global{
			Pkg:    pkg,
			name:   name,
			object: obj,
			typ:    types.NewPointer(obj.Type()), // address
			pos:    obj.Pos(),
		}
		pkg.objects[obj] = g
		if name != "_" {
			pkg.Members[name] = g
		}

	case *types.Func:
		sig := obj.Type().(*types.Signature)
		if sig.Recv() == nil && name == "init" {
			pkg.ninit++
			name = fmt.Sprintf("init#%d", pkg.ninit)
		}
		fn := createFunction(pkg.Prog, obj, name, syntax, pkg.info, goversion)
		fn.Pkg = pkg
		pkg.created = append(pkg.created, fn)
		pkg.objects[obj] = fn
		if name != "_" && sig.Recv() == nil {
			pkg.Members[name] = fn // package-level function
		}

	default: // (incl. *types.Package)
		panic("unexpected Object type: " + obj.String())
	}
}

// createFunction creates a function or method. It supports both
// CreatePackage (with or without syntax) and the on-demand creation
// of methods in non-created packages based on their types.Func.
func createFunction(prog *Program, obj *types.Func, name string, syntax ast.Node, info *types.Info, goversion string) *Function {
	sig := obj.Type().(*types.Signature)

	// Collect type parameters.
	var tparams *types.TypeParamList
	if rtparams := sig.RecvTypeParams(); rtparams.Len() > 0 {
		tparams = rtparams // method of generic type
	} else if sigparams := sig.TypeParams(); sigparams.Len() > 0 {
		tparams = sigparams // generic function
	}

	/* declared function/method (from syntax or export data) */
	fn := &Function{
		name:       name,
		object:     obj,
		Signature:  sig,
		build:      (*builder).buildFromSyntax,
		syntax:     syntax,
		info:       info,
		goversion:  goversion,
		pos:        obj.Pos(),
		Pkg:        nil, // may be set by caller
		Prog:       prog,
		typeparams: tparams,
	}
	if fn.syntax == nil {
		fn.Synthetic = "from type information"
		fn.build = (*builder).buildParamsOnly
	}
	if tparams.Len() > 0 {
		fn.generic = new(generic)
	}
	return fn
}

// membersFromDecl populates package pkg with members for each
// typechecker object (var, func, const or type) associated with the
// specified decl.
func membersFromDecl(pkg *Package, decl ast.Decl, goversion string) {
	switch decl := decl.(type) {
	case *ast.GenDecl: // import, const, type or var
		switch decl.Tok {
		case token.CONST:
			for _, spec := range decl.Specs {
				for _, id := range spec.(*ast.ValueSpec).Names {
					memberFromObject(pkg, pkg.info.Defs[id], nil, "")
				}
			}

		case token.VAR:
			for _, spec := range decl.Specs {
				for _, rhs := range spec.(*ast.ValueSpec).Values {
					pkg.initVersion[rhs] = goversion
				}
				for _, id := range spec.(*ast.ValueSpec).Names {
					memberFromObject(pkg, pkg.info.Defs[id], spec, goversion)
				}
			}

		case token.TYPE:
			for _, spec := range decl.Specs {
				id := spec.(*ast.TypeSpec).Name
				memberFromObject(pkg, pkg.info.Defs[id], nil, "")
			}
		}

	case *ast.FuncDecl:
		id := decl.Name
		memberFromObject(pkg, pkg.info.Defs[id], decl, goversion)
	}
}

// CreatePackage creates and returns an SSA Package from the
// specified type-checked, error-free file ASTs, and populates its
// Members mapping.
//
// importable determines whether this package should be returned by a
// subsequent call to ImportedPackage(pkg.Path()).
//
// The real work of building SSA form for each function is not done
// until a subsequent call to Package.Build.
//
// CreatePackage should not be called after building any package in
// the program.
func (prog *Program) CreatePackage(pkg *types.Package, files []*ast.File, info *types.Info, importable bool) *Package {
	// TODO(adonovan): assert that no package has yet been built.
	if pkg == nil {
		panic("nil pkg") // otherwise pkg.Scope below returns types.Universe!
	}
	p := &Package{
		Prog:    prog,
		Members: make(map[string]Member),
		objects: make(map[types.Object]Member),
		Pkg:     pkg,
		syntax:  info != nil,
		// transient values (cleared after Package.Build)
		info:        info,
		files:       files,
		initVersion: make(map[ast.Expr]string),
	}

	/* synthesized package initializer */
	p.init = &Function{
		name:      "init",
		Signature: new(types.Signature),
		Synthetic: "package initializer",
		Pkg:       p,
		Prog:      prog,
		build:     (*builder).buildPackageInit,
		info:      p.info,
		goversion: "", // See Package.build for details.
	}
	p.Members[p.init.name] = p.init
	p.created = append(p.created, p.init)

	// Allocate all package members: vars, funcs, consts and types.
	if len(files) > 0 {
		// Go source package.
		for _, file := range files {
			goversion := versions.Lang(versions.FileVersion(p.info, file))
			for _, decl := range file.Decls {
				membersFromDecl(p, decl, goversion)
			}
		}
	} else {
		// GC-compiled binary package (or "unsafe")
		// No code.
		// No position information.
		scope := p.Pkg.Scope()
		for _, name := range scope.Names() {
			obj := scope.Lookup(name)
			memberFromObject(p, obj, nil, "")
			if obj, ok := obj.(*types.TypeName); ok {
				// No Unalias: aliases should not duplicate methods.
				if named, ok := obj.Type().(*types.Named); ok {
					for i, n := 0, named.NumMethods(); i < n; i++ {
						memberFromObject(p, named.Method(i), nil, "")
					}
				}
			}
		}
	}

	if prog.mode&BareInits == 0 {
		// Add initializer guard variable.
		initguard := &Global{
			Pkg:  p,
			name: "init$guard",
			typ:  types.NewPointer(tBool),
		}
		p.Members[initguard.Name()] = initguard
	}

	if prog.mode&GlobalDebug != 0 {
		p.SetDebugMode(true)
	}

	if prog.mode&PrintPackages != 0 {
		printMu.Lock()
		p.WriteTo(os.Stdout)
		printMu.Unlock()
	}

	if importable {
		prog.imported[p.Pkg.Path()] = p
	}
	prog.packages[p.Pkg] = p

	return p
}

// printMu serializes printing of Packages/Functions to stdout.
var printMu sync.Mutex

// AllPackages returns a new slice containing all packages created by
// prog.CreatePackage in unspecified order.
func (prog *Program) AllPackages() []*Package {
	pkgs := make([]*Package, 0, len(prog.packages))
	for _, pkg := range prog.packages {
		pkgs = append(pkgs, pkg)
	}
	return pkgs
}

// ImportedPackage returns the importable Package whose PkgPath
// is path, or nil if no such Package has been created.
//
// A parameter to CreatePackage determines whether a package should be
// considered importable. For example, no import declaration can resolve
// to the ad-hoc main package created by 'go build foo.go'.
//
// TODO(adonovan): rethink this function and the "importable" concept;
// most packages are importable. This function assumes that all
// types.Package.Path values are unique within the ssa.Program, which is
// false---yet this function remains very convenient.
// Clients should use (*Program).Package instead where possible.
// SSA doesn't really need a string-keyed map of packages.
//
// Furthermore, the graph of packages may contain multiple variants
// (e.g. "p" vs "p as compiled for q.test"), and each has a different
// view of its dependencies.
func (prog *Program) ImportedPackage(path string) *Package {
	return prog.imported[path]
}
