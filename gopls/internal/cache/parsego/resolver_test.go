// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package parsego

import (
	"go/ast"
	"go/types"
	"os"
	"strings"
	"testing"

	"golang.org/x/tools/go/ast/astutil"
	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/gopls/internal/util/safetoken"
	"golang.org/x/tools/internal/testenv"
)

// TestGoplsSourceDoesNotUseObjectResolution verifies that gopls does not
// read fields that are set during syntactic object resolution, except in
// locations where we can guarantee that object resolution has occurred. This
// is achieved via static analysis of gopls source code to find references to
// the legacy Object symbols, checking the results against an allowlist
//
// Reading these fields would introduce a data race, due to the lazy
// resolution implemented by File.Resolve.
func TestGoplsSourceDoesNotUseObjectResolution(t *testing.T) {

	testenv.NeedsGoPackages(t)
	testenv.NeedsLocalXTools(t)

	cfg := &packages.Config{
		Mode: packages.NeedName | packages.NeedModule | packages.NeedCompiledGoFiles | packages.NeedTypes | packages.NeedTypesInfo | packages.NeedSyntax | packages.NeedImports | packages.NeedDeps,
	}
	cfg.Env = os.Environ()
	cfg.Env = append(cfg.Env,
		"GOPACKAGESDRIVER=off",
		"GOWORK=off", // necessary for -mod=mod below
		"GOFLAGS=-mod=mod",
	)

	pkgs, err := packages.Load(cfg,
		"go/ast",
		"golang.org/x/tools/go/ast/astutil",
		"golang.org/x/tools/gopls/...")

	if err != nil {
		t.Fatal(err)
	}
	var astPkg, astutilPkg *packages.Package
	for _, pkg := range pkgs {
		switch pkg.PkgPath {
		case "go/ast":
			astPkg = pkg
		case "golang.org/x/tools/go/ast/astutil":
			astutilPkg = pkg
		}
	}
	if astPkg == nil {
		t.Fatal("missing package go/ast")
	}
	if astutilPkg == nil {
		t.Fatal("missing package golang.org/x/tools/go/ast/astutil")
	}

	File := astPkg.Types.Scope().Lookup("File").Type()
	Ident := astPkg.Types.Scope().Lookup("Ident").Type()

	Scope, _, _ := types.LookupFieldOrMethod(File, true, astPkg.Types, "Scope")
	assert(Scope != nil, "nil Scope")
	Unresolved, _, _ := types.LookupFieldOrMethod(File, true, astPkg.Types, "Unresolved")
	assert(Unresolved != nil, "nil unresolved")
	Obj, _, _ := types.LookupFieldOrMethod(Ident, true, astPkg.Types, "Obj")
	assert(Obj != nil, "nil Obj")
	UsesImport := astutilPkg.Types.Scope().Lookup("UsesImport")
	assert(UsesImport != nil, "nil UsesImport")

	disallowed := map[types.Object]bool{
		Scope:      true,
		Unresolved: true,
		Obj:        true,
		UsesImport: true,
	}

	// exceptions catalogues packages or declarations that are allowed to use
	// forbidden symbols, with a rationale.
	//
	// - If the exception ends with '/', it is a prefix.
	// - If it ends with a qualified name, it is a declaration.
	// - Otherwise, it is an exact package path.
	//
	// TODO(rfindley): some sort of callgraph analysis would make these
	// exceptions much easier to maintain.
	exceptions := []string{
		"golang.org/x/tools/go/analysis/passes/",                             // analyzers may rely on object resolution
		"golang.org/x/tools/gopls/internal/analysis/simplifyslice",           // restrict ourselves to one blessed analyzer
		"golang.org/x/tools/gopls/internal/cache/parsego",                    // used by parsego.File.Resolve, of course
		"golang.org/x/tools/gopls/internal/golang.builtinDecl",               // the builtin file is resolved
		"golang.org/x/tools/gopls/internal/golang.NewBuiltinSignature",       // ditto
		"golang.org/x/tools/gopls/internal/golang/completion.builtinArgKind", // ditto
		"golang.org/x/tools/internal/imports",                                // goimports does its own parsing
		"golang.org/x/tools/go/ast/astutil.UsesImport",                       // disallowed
		"golang.org/x/tools/go/ast/astutil.isTopName",                        // only reached from astutil.UsesImport
		"go/ast",
		"go/parser",
		"go/doc", // manually verified that our usage is safe
	}

	packages.Visit(pkgs, nil, func(pkg *packages.Package) {
		for _, exception := range exceptions {
			if strings.HasSuffix(exception, "/") {
				if strings.HasPrefix(pkg.PkgPath, exception) {
					return
				}
			} else if pkg.PkgPath == exception {
				return
			}
		}

	searchUses:
		for ident, obj := range pkg.TypesInfo.Uses {
			if disallowed[obj] {
				decl := findEnclosingFuncDecl(ident, pkg)
				if decl == "" {
					posn := safetoken.Position(pkg.Fset.File(ident.Pos()), ident.Pos())
					t.Fatalf("%s: couldn't find enclosing decl for use of %s", posn, ident.Name)
				}
				qualified := pkg.PkgPath + "." + decl
				for _, exception := range exceptions {
					if exception == qualified {
						continue searchUses
					}
				}
				posn := safetoken.StartPosition(pkg.Fset, ident.Pos())
				t.Errorf("%s: forbidden use of %v in %s", posn, obj, qualified)
			}
		}
	})
}

// findEnclosingFuncDecl finds the name of the func decl enclosing the usage,
// or "".
//
// (Usage could theoretically exist in e.g. var initializers, but that would be
// odd.)
func findEnclosingFuncDecl(ident *ast.Ident, pkg *packages.Package) string {
	for _, file := range pkg.Syntax {
		if file.FileStart <= ident.Pos() && ident.Pos() < file.FileEnd {
			path, _ := astutil.PathEnclosingInterval(file, ident.Pos(), ident.End())
			decl, ok := path[len(path)-2].(*ast.FuncDecl)
			if ok {
				return decl.Name.Name
			}
		}
	}
	return ""
}
