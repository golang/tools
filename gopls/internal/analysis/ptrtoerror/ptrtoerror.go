// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ptrtoerror

import (
	_ "embed"
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"path/filepath"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/passes/inspect"
	"golang.org/x/tools/go/ast/inspector"
	"golang.org/x/tools/gopls/internal/util/safetoken"
	"golang.org/x/tools/internal/analysis/analyzerutil"
)

//go:embed doc.go
var doc string

// Analyzer detects inconsistent conversions of concrete types to error.
var Analyzer = &analysis.Analyzer{
	Name:      "ptrtoerror",
	Doc:       analyzerutil.MustExtractDoc(doc, "ptrtoerror"),
	URL:       "https://pkg.go.dev/golang.org/x/tools/gopls/internal/analysis/ptrtoerror",
	Requires:  []*analysis.Analyzer{inspect.Analyzer},
	FactTypes: []analysis.Fact{(*isErrorFact)(nil)},
	Run:       run,
}

// run executes the ptrtoerror analysis pass.
func run(pass *analysis.Pass) (any, error) {
	var (
		inspect = pass.ResultOf[inspect.Analyzer].(*inspector.Inspector)
		info    = pass.TypesInfo
	)

	type localType struct{ val, ptr []conversion } // all conversions from local type E, *E to error
	var (
		localTypes    = make(map[*types.TypeName]*localType)   // error conversion info about types from this package
		importedTypes = make(map[*types.TypeName]*isErrorFact) // error conversion info about imported types (nil => unknown)
	)
	for conv := range conversions(inspect.Root(), info) {
		if conv.tLHS == nil {
			panic("nil LHS type")
		}
		if conv.tRHS == nil {
			panic("nil RHS type")
		}
		if conv.expr == nil {
			panic("nil Expr")
		}

		if !types.IsInterface(conv.tLHS) ||
			!types.AssignableTo(conv.tLHS, builtinError.Type()) {
			continue
		}

		// E or *E?
		t := conv.tRHS
		ptr, isPtr := types.Unalias(t).(*types.Pointer)
		if isPtr {
			t = ptr.Elem()
		}
		named, ok := types.Unalias(t).(*types.Named)
		if !ok || types.IsInterface(named) {
			continue // not a concrete named type
		}
		tname := named.Obj()

		// local type?
		if tname.Pkg() == pass.Pkg {
			ltinfo, ok := localTypes[tname]
			if !ok {
				ltinfo = new(localType)
				localTypes[tname] = ltinfo
			}
			// Save it for the second pass.
			list := cond(isPtr, &ltinfo.ptr, &ltinfo.val)
			*list = append(*list, conv)
			continue
		}

		// imported type
		f, ok := importedTypes[tname]
		if !ok {
			var fact isErrorFact
			if pass.ImportObjectFact(tname, &fact) {
				f = &fact
			}
			importedTypes[tname] = f // memoize even if nil
		}
		if f != nil && f.Pointer != isPtr {
			var message string
			if isPtr {
				message = fmt.Sprintf("conversion of *%s to error, but package %q uses %s (sans pointer) as an error (e.g. at %s)",
					types.TypeString(named, (*types.Package).Name),
					tname.Pkg().Path(),
					tname.Name(),
					f.Where)
			} else {
				message = fmt.Sprintf("conversion of %s to error, but package %q uses pointer *%s as an error (e.g. at %s)",
					types.TypeString(named, (*types.Package).Name),
					tname.Pkg().Path(),
					tname.Name(),
					f.Where)
			}
			pass.Report(analysis.Diagnostic{
				Pos:     conv.expr.Pos(),
				End:     conv.expr.End(),
				Message: message,
			})
		}
	}

	// Second pass over conversions of local types.
	for tname, ltinfo := range localTypes {
		isPtr := ltinfo.ptr != nil

		// If used as both E and *E,
		// report all conversions as ambiguous.
		if isPtr && ltinfo.val != nil {
			conflicts := func(convs []conversion, other conversion, otherKind string) {
				for _, conv := range convs {
					pass.Report(analysis.Diagnostic{
						Pos:     conv.expr.Pos(),
						End:     conv.expr.End(),
						Message: fmt.Sprintf("%s is converted to error both as a value and as a pointer", tname.Name()),
						Related: []analysis.RelatedInformation{{
							Pos:     other.expr.Pos(),
							End:     other.expr.End(),
							Message: fmt.Sprintf("converted as a %s here", otherKind),
						}},
					})
				}
			}
			conflicts(ltinfo.ptr, ltinfo.val[0], "value")
			conflicts(ltinfo.val, ltinfo.ptr[0], "pointer")
			continue
		}

		// E or *E is used consistently.
		// Export a fact.
		if tname.Exported() {
			var first conversion
			if isPtr {
				first = ltinfo.ptr[0]
			} else {
				first = ltinfo.val[0]
			}
			posn := safetoken.StartPosition(pass.Fset, first.expr.Pos())
			where := fmt.Sprintf("%s:%d:%d", filepath.Base(posn.Filename), posn.Line, posn.Column)
			pass.ExportObjectFact(tname, &isErrorFact{
				Pointer: isPtr,
				Where:   where,
			})
		}
	}

	// Report a diagnostic for each local named type E such that E
	// and *E implement error yet neither type is converted to error
	// (which would indicate intent).
	for curTypeSpec := range inspect.Root().Preorder((*ast.TypeSpec)(nil)) {
		tspec := curTypeSpec.Node().(*ast.TypeSpec)
		if tspec.Assign.IsValid() {
			continue // ignore aliases (need more thorough treatment)
		}
		tname := info.Defs[tspec.Name].(*types.TypeName)
		if _, ok := localTypes[tname]; ok {
			continue // E or *E was converted to error
		}
		if types.IsInterface(tname.Type()) || !types.AssignableTo(tname.Type(), builtinError.Type()) {
			continue
		}

		// Both E and (implicitly) *E implement error
		// but neither conversion was used in this package.

		// Suggest a fix to insert 'var _ error = ...' after
		// the enclosing type decl to declare the intent.
		var fixes []analysis.SuggestedFix
		if curIdent, ok := inspect.Root().FindByPos(tname.Pos(), tname.Pos()); ok {
			for curDecl := range curIdent.Enclosing((*ast.GenDecl)(nil)) {
				pos := curDecl.Node().End()
				fixes = []analysis.SuggestedFix{
					{
						Message: fmt.Sprintf(`Declare that %s implements error`, tname.Name()),
						TextEdits: []analysis.TextEdit{{
							Pos:     pos,
							End:     pos,
							NewText: fmt.Appendf(nil, "\n\nvar _ error = *new(%s)\n", tname.Name()),
						}},
					},
					{
						Message: fmt.Sprintf(`Declare that *%s implements error`, tname.Name()),
						TextEdits: []analysis.TextEdit{{
							Pos:     pos,
							End:     pos,
							NewText: fmt.Appendf(nil, "\n\nvar _ error = (*%s)(nil)\n", tname.Name()),
						}},
					},
				}
				break
			}
		}
		pass.Report(analysis.Diagnostic{
			Pos:            tname.Pos(),
			End:            tname.Pos() + token.Pos(len(tname.Name())),
			Message:        fmt.Sprintf("both %[1]s and *%[1]s implement the error interface, making the intent ambiguous", tname.Name()),
			SuggestedFixes: fixes,
		})
	}

	return nil, nil
}

// An isErrorFact associated with an exported type E indicates that
// either E or *E (depending on the value of Pointer) implements error.
type isErrorFact struct {
	Pointer bool   // the preferred error type is *E, not E
	Where   string // a hint to where the fact was established of the form "foo.go:123:45"
}

func (f *isErrorFact) AFact()         {}
func (f *isErrorFact) String() string { return cond(f.Pointer, "*E", "E") }

// -- helpers --

var builtinError = types.Universe.Lookup("error")

func cond[T any](cond bool, t, f T) T {
	if cond {
		return t
	} else {
		return f
	}
}
