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
	"golang.org/x/tools/internal/analysis/analyzerutil"
	typeindexanalyzer "golang.org/x/tools/internal/analysis/typeindex"
	"golang.org/x/tools/internal/astutil"
	"golang.org/x/tools/internal/packagepath"
	"golang.org/x/tools/internal/refactor"
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
//
// For enums (defined here as const decls where all consts have the same type),
// we only require that one of the names in the group is used, since it is
// common for at least some values to be unused when they are added for
// symmetry, future use, or to conform to some external pattern.

//go:embed doc.go
var doc string

var Analyzer = &analysis.Analyzer{
	Name:     "unusedfunc",
	Doc:      analyzerutil.MustExtractDoc(doc, "unusedfunc"),
	Requires: []*analysis.Analyzer{inspect.Analyzer, typeindexanalyzer.Analyzer},
	Run:      run,
	URL:      "https://pkg.go.dev/golang.org/x/tools/gopls/internal/analysis/unusedfunc",
}

func run(pass *analysis.Pass) (any, error) {
	// The standard library makes heavy use of intrinsics, linknames, etc,
	// that confuse this algorithm; so skip it (#74130).
	if packagepath.IsStdPackage(pass.Pkg.Path()) {
		return nil, nil
	}

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

	// used reports whether the object declared at id is (potentially) used.
	// References within curSelf are ignored.
	used := func(id *ast.Ident, curSelf inspector.Cursor) bool {
		// Exported functions may be called from other packages.
		if id.IsExported() {
			return true
		}

		// Blank functions are exempt from diagnostics.
		if id.Name == "_" {
			return true
		}

		// Check for uses (including selections).
		obj := pass.TypesInfo.Defs[id]
		for curId := range index.Uses(obj) {
			// Ignore self references.
			if !curSelf.Contains(curId) {
				return true // symbol is referenced
			}
		}
		return false
	}

	// checkUnused reports a diagnostic if the object declared at id
	// is unexported and unused. References within curSelf are ignored.
	checkUnused := func(noun string, id *ast.Ident, curSelf inspector.Cursor, delete func() []analysis.TextEdit) {
		if used(id, curSelf) {
			return
		}

		pass.Report(analysis.Diagnostic{
			Pos:     id.Pos(),
			End:     id.End(),
			Message: fmt.Sprintf("%s %q is unused", noun, id.Name),
			SuggestedFixes: []analysis.SuggestedFix{{
				Message:   fmt.Sprintf("Delete %s %q", noun, id.Name),
				TextEdits: delete(),
			}},
		})
	}

	// isEnum returns true if the decl curGenDecl is a const decl with more than one
	// spec where all consts are the same type.
	isEnum := func(curGenDecl inspector.Cursor) bool {
		decl := curGenDecl.Node().(*ast.GenDecl)
		if decl.Tok != token.CONST || len(decl.Specs) < 2 {
			return false
		}
		var prevType types.Type
		for _, spec := range decl.Specs {
			spec := spec.(*ast.ValueSpec)
			for _, id := range spec.Names {
				curType := pass.TypesInfo.TypeOf(id)
				if prevType != nil && !types.Identical(curType, prevType) {
					return false
				}
				prevType = curType
			}
		}
		return true
	}

	// Check each package-level declaration (and method) for uses.
	for curFile := range inspect.Root().Preorder((*ast.File)(nil)) {
		file := curFile.Node().(*ast.File)
		if ast.IsGenerated(file) {
			continue // skip generated files
		}
		tokFile := pass.Fset.File(file.Pos())

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
			if doc := astutil.DocComment(decl); doc != nil {
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
				checkUnused(noun, decl.Name, curDecl, func() []analysis.TextEdit {
					return refactor.DeleteDecl(tokFile, curDecl)
				})

			case *ast.GenDecl:
				switch decl.Tok {
				case token.TYPE:
					for i, spec := range decl.Specs {
						var (
							spec    = spec.(*ast.TypeSpec)
							id      = spec.Name
							curSpec = curDecl.ChildAt(edge.GenDecl_Specs, i)
						)
						checkUnused("type", id, curSpec, func() []analysis.TextEdit {
							return refactor.DeleteSpec(tokFile, curSpec)
						})
					}

				case token.CONST, token.VAR:
					if isEnum(curDecl) {
						// For enum-like constants, a use of any
						// name acts as a use of the whole.
						// TODO(mkalil): This results in false negatives, for example:
						// const (
						//	 a = 0
						//	 b = a + 1
						// )
						// We report that a is "used" even though is not used outside the const
						// decl, and since we do not report errors in enums that have at least one
						// used name, we do not report a diagnostic at all.
						allUnused := true
						for i, spec := range decl.Specs {
							curSpec := curDecl.ChildAt(edge.GenDecl_Specs, i)
							for _, id := range spec.(*ast.ValueSpec).Names {
								if used(id, curSpec) {
									allUnused = false
									break
								}
							}
						}
						if allUnused {
							edits := refactor.DeleteDecl(tokFile, curDecl)
							pass.Report(analysis.Diagnostic{
								Pos:     decl.Pos(),
								End:     decl.End(),
								Message: "all values in this set of constants are unused",
								SuggestedFixes: []analysis.SuggestedFix{{
									Message:   "Delete the constants declaration",
									TextEdits: edits,
								}},
							})
						}
					} else {
						for i, spec := range decl.Specs {
							spec := spec.(*ast.ValueSpec)
							curSpec := curDecl.ChildAt(edge.GenDecl_Specs, i)

							for j, id := range spec.Names {
								checkUnused(decl.Tok.String(), id, curSpec, func() []analysis.TextEdit {
									curId := curSpec.ChildAt(edge.ValueSpec_Names, j)
									return refactor.DeleteVar(tokFile, pass.TypesInfo, curId)
								})
							}
						}
					}
				}
			}
		}
	}

	return nil, nil
}

func cond[T any](cond bool, t, f T) T {
	if cond {
		return t
	} else {
		return f
	}
}
