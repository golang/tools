// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package unusedfunc

import (
	_ "embed"
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"strings"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/passes/inspect"
	"golang.org/x/tools/go/ast/edge"
	"golang.org/x/tools/go/ast/inspector"
	"golang.org/x/tools/internal/analysisinternal"
	typeindexanalyzer "golang.org/x/tools/internal/analysisinternal/typeindex"
	"golang.org/x/tools/internal/typesinternal/typeindex"
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
//
// Types (sans methods), constants, and vars are more straightforward.
// For now we ignore enums (const decls using iota) since it is
// commmon for at least some values to be unused when they are added
// for symmetry, future use, or to conform to some external pattern.

//go:embed doc.go
var doc string

var Analyzer = &analysis.Analyzer{
	Name:     "unusedfunc",
	Doc:      analysisinternal.MustExtractDoc(doc, "unusedfunc"),
	Requires: []*analysis.Analyzer{inspect.Analyzer, typeindexanalyzer.Analyzer},
	Run:      run,
	URL:      "https://pkg.go.dev/golang.org/x/tools/gopls/internal/analysis/unusedfunc",
}

func run(pass *analysis.Pass) (any, error) {
	var (
		inspect = pass.ResultOf[inspect.Analyzer].(*inspector.Inspector)
		index   = pass.ResultOf[typeindexanalyzer.Analyzer].(*typeindex.Index)
	)

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

	// checkUnused reports a diagnostic if the object declared at id
	// is unexported and unused. References within curSelf are ignored.
	checkUnused := func(noun string, id *ast.Ident, node ast.Node, curSelf inspector.Cursor) {
		// Exported functions may be called from other packages.
		if id.IsExported() {
			return
		}

		// Blank functions are exempt from diagnostics.
		if id.Name == "_" {
			return
		}

		// Check for uses (including selections).
		obj := pass.TypesInfo.Defs[id]
		for curId := range index.Uses(obj) {
			// Ignore self references.
			if !curSelf.Contains(curId) {
				return // symbol is referenced
			}
		}

		// Expand to include leading doc comment.
		pos := node.Pos()
		if doc := docComment(node); doc != nil {
			pos = doc.Pos()
		}

		// Expand to include trailing line  comment.
		end := node.End()
		if doc := eolComment(node); doc != nil {
			end = doc.End()
		}

		pass.Report(analysis.Diagnostic{
			Pos:     id.Pos(),
			End:     id.End(),
			Message: fmt.Sprintf("%s %q is unused", noun, id.Name),
			SuggestedFixes: []analysis.SuggestedFix{{
				Message: fmt.Sprintf("Delete %s %q", noun, id.Name),
				TextEdits: []analysis.TextEdit{{
					Pos: pos,
					End: end,
				}},
			}},
		})
	}

	// Gather the set of enums (const GenDecls that use iota).
	enums := make(map[inspector.Cursor]bool)
	for curId := range index.Uses(types.Universe.Lookup("iota")) {
		for curDecl := range curId.Enclosing((*ast.GenDecl)(nil)) {
			enums[curDecl] = true
			break
		}
	}

	// Check each package-level declaration (and method) for uses.
	for curFile := range inspect.Root().Preorder((*ast.File)(nil)) {
		file := curFile.Node().(*ast.File)
		if ast.IsGenerated(file) {
			continue // skip generated files
		}

	nextDecl:
		for i := range file.Decls {
			curDecl := curFile.ChildAt(edge.File_Decls, i)
			decl := curDecl.Node().(ast.Decl)

			// Skip if there's a preceding //go:linkname directive.
			// (This is relevant only to func and var decls.)
			//
			// (A program can link fine without such a directive,
			// but it is bad style; and the directive may
			// appear anywhere, not just on the preceding line,
			// but again that is poor form.)
			if doc := docComment(decl); doc != nil {
				for _, comment := range doc.List {
					// TODO(adonovan): use ast.ParseDirective when #68021 lands.
					if strings.HasPrefix(comment.Text, "//go:linkname ") {
						continue nextDecl
					}
				}
			}

			switch decl := decl.(type) {
			case *ast.FuncDecl:
				id := decl.Name
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

				noun := cond(decl.Recv == nil, "function", "method")
				checkUnused(noun, decl.Name, decl, curDecl)

			case *ast.GenDecl:
				// Instead of deleting a spec in a singleton decl,
				// delete the whole decl.
				singleton := len(decl.Specs) == 1

				switch decl.Tok {
				case token.TYPE:
					for i, spec := range decl.Specs {
						var (
							spec    = spec.(*ast.TypeSpec)
							id      = spec.Name
							curSelf = curDecl.ChildAt(edge.GenDecl_Specs, i)
						)
						checkUnused("type", id, cond[ast.Node](singleton, decl, spec), curSelf)
					}

				case token.CONST, token.VAR:
					// Skip enums: values are often unused.
					if enums[curDecl] {
						continue
					}
					for i, spec := range decl.Specs {
						spec := spec.(*ast.ValueSpec)
						curSpec := curDecl.ChildAt(edge.GenDecl_Specs, i)

						// Ignore n:n and n:1 assignments for now.
						// TODO(adonovan): support these cases.
						if len(spec.Names) != 1 {
							continue
						}
						id := spec.Names[0]
						checkUnused(decl.Tok.String(), id, cond[ast.Node](singleton, decl, spec), curSpec)
					}
				}
			}
		}
	}

	return nil, nil
}

func docComment(n ast.Node) *ast.CommentGroup {
	switch n := n.(type) {
	case *ast.FuncDecl:
		return n.Doc
	case *ast.GenDecl:
		return n.Doc
	case *ast.ValueSpec:
		return n.Doc
	case *ast.TypeSpec:
		return n.Doc
	}
	return nil // includes File, ImportSpec, Field
}

func eolComment(n ast.Node) *ast.CommentGroup {
	// TODO(adonovan): support:
	//    func f() {...} // comment
	switch n := n.(type) {
	case *ast.GenDecl:
		if !n.TokPos.IsValid() && len(n.Specs) == 1 {
			return eolComment(n.Specs[0])
		}
	case *ast.ValueSpec:
		return n.Comment
	case *ast.TypeSpec:
		return n.Comment
	}
	return nil
}

func cond[T any](cond bool, t, f T) T {
	if cond {
		return t
	} else {
		return f
	}
}
