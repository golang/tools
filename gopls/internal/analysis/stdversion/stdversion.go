// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package stdversion reports uses of standard library symbols that are
// "too new" for the Go version in force in the referring file.
package stdversion

import (
	"go/ast"
	"go/build"
	"go/types"
	"regexp"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/passes/inspect"
	"golang.org/x/tools/go/ast/inspector"
	"golang.org/x/tools/gopls/internal/util/slices"
	"golang.org/x/tools/internal/stdlib"
	"golang.org/x/tools/internal/versions"
)

const Doc = `report uses of too-new standard library symbols

The stdversion analyzer reports references to symbols in the standard
library that were introduced by a Go release higher than the one in
force in the referring file. (Recall that the file's Go version is
defined by the 'go' directive its module's go.mod file, or by a
"//go:build go1.X" build tag at the top of the file.)

The analyzer does not report a diagnostic for a reference to a "too
new" field or method of a type that is itself "too new", as this may
have false positives, for example if fields or methods are accessed
through a type alias that is guarded by a Go version constraint.
`

var Analyzer = &analysis.Analyzer{
	Name:     "stdversion",
	Doc:      Doc,
	Requires: []*analysis.Analyzer{inspect.Analyzer},
	Run:      run,
}

func run(pass *analysis.Pass) (any, error) {
	// Prior to go1.22, versions.FileVersion returns only the
	// toolchain version, which is of no use to us, so
	// disable this analyzer on earlier versions.
	if !slices.Contains(build.Default.ReleaseTags, "go1.22") {
		return nil, nil
	}

	// disallowedSymbolsMemo returns the set of standard library symbols
	// in a given package that are disallowed at the specified Go version.
	type key struct {
		pkg     *types.Package
		version string
	}
	memo := make(map[key]map[types.Object]string) // records symbol's minimum Go version
	disallowedSymbolsMemo := func(pkg *types.Package, version string) map[types.Object]string {
		k := key{pkg, version}
		disallowed, ok := memo[k]
		if !ok {
			disallowed = disallowedSymbols(pkg, version)
			memo[k] = disallowed
		}
		return disallowed
	}

	// TODO(adonovan): after go1.21, call GoVersion directly.
	pkgVersion := any(pass.Pkg).(interface{ GoVersion() string }).GoVersion()

	// Scan the syntax looking for references to symbols
	// that are disallowed by the version of the file.
	inspect := pass.ResultOf[inspect.Analyzer].(*inspector.Inspector)
	nodeFilter := []ast.Node{
		(*ast.File)(nil),
		(*ast.Ident)(nil),
	}
	var fileVersion string // "" => no check
	inspect.Preorder(nodeFilter, func(n ast.Node) {
		switch n := n.(type) {
		case *ast.File:
			if isGenerated(n) {
				// Suppress diagnostics in generated files (such as cgo).
				fileVersion = ""
			} else {
				fileVersion = versions.Lang(versions.FileVersion(pass.TypesInfo, n))
				// (may be "" if unknown)
			}

		case *ast.Ident:
			if fileVersion != "" {
				if obj, ok := pass.TypesInfo.Uses[n]; ok && obj.Pkg() != nil {
					disallowed := disallowedSymbolsMemo(obj.Pkg(), fileVersion)
					if minVersion, ok := disallowed[origin(obj)]; ok {
						noun := "module"
						if fileVersion != pkgVersion {
							noun = "file"
						}
						pass.ReportRangef(n, "%s.%s requires %v or later (%s is %s)",
							obj.Pkg().Name(), obj.Name(), minVersion, noun, fileVersion)
					}
				}
			}
		}
	})
	return nil, nil
}

// disallowedSymbols computes the set of package-level symbols
// exported by direct imports of pkg that are not available at the
// specified version. The result maps each symbol to its minimum version.
func disallowedSymbols(pkg *types.Package, version string) map[types.Object]string {
	disallowed := make(map[types.Object]string)

	// Pass 1: package-level symbols.
	symbols := stdlib.PackageSymbols[pkg.Path()]
	for _, sym := range symbols {
		symver := sym.Version.String()
		if versions.Before(version, symver) {
			switch sym.Kind {
			case stdlib.Func, stdlib.Var, stdlib.Const, stdlib.Type:
				disallowed[pkg.Scope().Lookup(sym.Name)] = symver
			}
		}
	}

	// Pass 2: fields and methods.
	//
	// We allow fields and methods if their associated type is
	// disallowed, as otherwise we would report false positives
	// for compatibility shims. Consider:
	//
	//   //go:build go1.22
	//   type T struct { F std.Real } // correct new API
	//
	//   //go:build !go1.22
	//   type T struct { F fake } // shim
	//   type fake struct { ... }
	//   func (fake) M () {}
	//
	// These alternative declarations of T use either the std.Real
	// type, introduced in go1.22, or a fake type, for the field
	// F. (The fakery could be arbitrarily deep, involving more
	// nested fields and methods than are shown here.) Clients
	// that use the compatibility shim T will compile with any
	// version of go, whether older or newer than go1.22, but only
	// the newer version will use the std.Real implementation.
	//
	// Now consider a reference to method M in new(T).F.M() in a
	// module that requires a minimum of go1.21. The analysis may
	// occur using a version of Go higher than 1.21, selecting the
	// first version of T, so the method M is Real.M. This would
	// spuriously cause the analyzer to report a reference to a
	// too-new symbol even though this expression compiles just
	// fine (with the fake implementation) using go1.21.
	for _, sym := range symbols {
		symVersion := sym.Version.String()
		if !versions.Before(version, symVersion) {
			continue // allowed
		}

		var obj types.Object
		switch sym.Kind {
		case stdlib.Field:
			typename, name := sym.SplitField()
			t := pkg.Scope().Lookup(typename)
			if disallowed[t] == "" {
				obj, _, _ = types.LookupFieldOrMethod(t.Type(), false, pkg, name)
			}

		case stdlib.Method:
			ptr, recvname, name := sym.SplitMethod()
			t := pkg.Scope().Lookup(recvname)
			if disallowed[t] == "" {
				obj, _, _ = types.LookupFieldOrMethod(t.Type(), ptr, pkg, name)
			}
		}
		if obj != nil {
			disallowed[obj] = symVersion
		}
	}

	return disallowed
}

// Reduced from ../../golang/util.go. Good enough for now.
// TODO(adonovan): use ast.IsGenerated in go1.21.
func isGenerated(f *ast.File) bool {
	for _, group := range f.Comments {
		for _, comment := range group.List {
			if matched := generatedRx.MatchString(comment.Text); matched {
				return true
			}
		}
	}
	return false
}

// Matches cgo generated comment as well as the proposed standard:
//
//	https://golang.org/s/generatedcode
var generatedRx = regexp.MustCompile(`// .*DO NOT EDIT\.?`)

// origin returns the original uninstantiated symbol for obj.
func origin(obj types.Object) types.Object {
	switch obj := obj.(type) {
	case *types.Var:
		return obj.Origin()
	case *types.Func:
		return obj.Origin()
	case *types.TypeName:
		if named, ok := obj.Type().(*types.Named); ok { // (don't unalias)
			return named.Origin().Obj()
		}
	}
	return obj
}
