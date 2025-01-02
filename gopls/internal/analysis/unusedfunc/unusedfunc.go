// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package unusedfunc

import (
	_ "embed"
	"fmt"
	"go/ast"
	"go/types"
	"strings"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/passes/inspect"
	"golang.org/x/tools/go/ast/inspector"
	"golang.org/x/tools/gopls/internal/util/astutil"
	"golang.org/x/tools/internal/analysisinternal"
)

// Assumptions
//
// Like unusedparams, this analyzer depends on the invariant of the
// gopls analysis driver that only the "widest" package (the one with
// the most files) for a given file is analyzed. This invariant allows
// the algorithm to make "closed world" assumptions about the target
// package. (In general, analysis of Go test packages cannot make that
// assumption because in-package tests add new files to existing
// packages, potentially invalidating results.) Consequently, running
// this analyzer in, say, unitchecker or multichecker may produce
// incorrect results.
//
// A function is unreferenced if it is never referenced except within
// its own declaration, and it is unexported. (Exported functions must
// be assumed to be referenced from other packages.)
//
// For methods, we assume that the receiver type is "live" (variables
// of that type are created) and "address taken" (its rtype ends up in
// an at least one interface value). This means exported methods may
// be called via reflection or by interfaces defined in other
// packages, so again we are concerned only with unexported methods.
//
// To discount the possibility of a method being called via an
// interface, we must additionally ensure that no literal interface
// type within the package has a method of the same name.
// (Unexported methods cannot be called through interfaces declared
// in other packages because each package has a private namespace
// for unexported identifiers.)

//go:embed doc.go
var doc string

var Analyzer = &analysis.Analyzer{
	Name:     "unusedfunc",
	Doc:      analysisinternal.MustExtractDoc(doc, "unusedfunc"),
	Requires: []*analysis.Analyzer{inspect.Analyzer},
	Run:      run,
	URL:      "https://pkg.go.dev/golang.org/x/tools/gopls/internal/analysis/unusedfunc",
}

func run(pass *analysis.Pass) (any, error) {
	inspect := pass.ResultOf[inspect.Analyzer].(*inspector.Inspector)

	// Gather names of unexported interface methods declared in this package.
	localIfaceMethods := make(map[string]bool)
	nodeFilter := []ast.Node{(*ast.InterfaceType)(nil)}
	inspect.Preorder(nodeFilter, func(n ast.Node) {
		iface := n.(*ast.InterfaceType)
		for _, field := range iface.Methods.List {
			if len(field.Names) > 0 {
				id := field.Names[0]
				if !id.IsExported() {
					// TODO(adonovan): check not just name but signature too.
					localIfaceMethods[id.Name] = true
				}
			}
		}
	})

	// Map each function/method symbol to its declaration.
	decls := make(map[*types.Func]*ast.FuncDecl)
	for _, file := range pass.Files {
		if ast.IsGenerated(file) {
			continue // skip generated files
		}

		for _, decl := range file.Decls {
			if decl, ok := decl.(*ast.FuncDecl); ok {
				id := decl.Name
				// Exported functions may be called from other packages.
				if id.IsExported() {
					continue
				}

				// Blank functions are exempt from diagnostics.
				if id.Name == "_" {
					continue
				}

				// An (unexported) method whose name matches an
				// interface method declared in the same package
				// may be dynamically called via that interface.
				if decl.Recv != nil && localIfaceMethods[id.Name] {
					continue
				}

				// main and init functions are implicitly always used
				if decl.Recv == nil && (id.Name == "init" || id.Name == "main") {
					continue
				}

				fn := pass.TypesInfo.Defs[id].(*types.Func)
				decls[fn] = decl
			}
		}
	}

	// Scan for uses of each function symbol.
	// (Ignore uses within the function's body.)
	use := func(ref ast.Node, obj types.Object) {
		if fn, ok := obj.(*types.Func); ok {
			if fn := fn.Origin(); fn.Pkg() == pass.Pkg {
				if decl, ok := decls[fn]; ok {
					// Ignore uses within the function's body.
					if decl.Body != nil && astutil.NodeContains(decl.Body, ref.Pos()) {
						return
					}
					delete(decls, fn) // symbol is referenced
				}
			}
		}
	}
	for id, obj := range pass.TypesInfo.Uses {
		use(id, obj)
	}
	for sel, seln := range pass.TypesInfo.Selections {
		use(sel, seln.Obj())
	}

	// Report the remaining unreferenced symbols.
nextDecl:
	for fn, decl := range decls {
		noun := "function"
		if decl.Recv != nil {
			noun = "method"
		}

		pos := decl.Pos() // start of func decl or associated comment
		if decl.Doc != nil {
			pos = decl.Doc.Pos()

			// Skip if there's a preceding //go:linkname directive.
			//
			// (A program can link fine without such a directive,
			// but it is bad style; and the directive may
			// appear anywhere, not just on the preceding line,
			// but again that is poor form.)
			//
			// TODO(adonovan): use ast.ParseDirective when #68021 lands.
			for _, comment := range decl.Doc.List {
				if strings.HasPrefix(comment.Text, "//go:linkname ") {
					continue nextDecl
				}
			}
		}

		pass.Report(analysis.Diagnostic{
			Pos:     decl.Name.Pos(),
			End:     decl.Name.End(),
			Message: fmt.Sprintf("%s %q is unused", noun, fn.Name()),
			SuggestedFixes: []analysis.SuggestedFix{{
				Message: fmt.Sprintf("Delete %s %q", noun, fn.Name()),
				TextEdits: []analysis.TextEdit{{
					// delete declaration
					Pos: pos,
					End: decl.End(),
				}},
			}},
		})
	}

	return nil, nil
}
