// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package fillstruct defines an Analyzer that automatically
// fills in a struct declaration with zero value elements for each field.
//
// The analyzer's diagnostic is merely a prompt.
// The actual fix is created by a separate direct call from gopls to
// the SuggestedFixes function.
// Tests of Analyzer.Run can be found in ./testdata/src.
// Tests of the SuggestedFixes logic live in ../../testdata/fillstruct.
package fillstruct

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/format"
	"go/printer"
	"go/token"
	"go/types"
	"slices"
	"strings"
	"unicode"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/ast/inspector"
	"golang.org/x/tools/gopls/internal/analysis/fillreturns"
	"golang.org/x/tools/gopls/internal/cache"
	"golang.org/x/tools/gopls/internal/cache/parsego"
	"golang.org/x/tools/gopls/internal/fuzzy"
	"golang.org/x/tools/gopls/internal/util/cursorutil"
	"golang.org/x/tools/gopls/internal/util/safetoken"
	"golang.org/x/tools/internal/astutil"
	"golang.org/x/tools/internal/typeparams"
	"golang.org/x/tools/internal/typesinternal"
)

// Diagnose computes a diagnostic for the enclosing struct literal enclosing
// the provided start and end position of curFile.
//
// If the target struct is already fully populated, no diagnostic is reported.
//
// The diagnostic contains a lazy fix; the actual patch is computed
// (via the ApplyFix command) by a call to [SuggestedFix].
func Diagnose(curFile inspector.Cursor, start, end token.Pos, pkg *types.Package, info *types.Info) (diags []analysis.Diagnostic) {
	cur, _, _, _ := astutil.Select(curFile, start, end)

	var lits []*ast.CompositeLit
	for c := range cur.Enclosing((*ast.CompositeLit)(nil)) {
		lits = append(lits, c.Node().(*ast.CompositeLit))
	}
	for c := range cur.Preorder((*ast.CompositeLit)(nil)) {
		expr := c.Node().(*ast.CompositeLit)
		// Avoid double-counting when cur.Node() is itself a [ast.CompositeLit].
		if expr == cur.Node() {
			continue
		}
		if expr.Pos() <= end && expr.End() >= start {
			lits = append(lits, expr)
		}
	}

nextComp:
	for _, expr := range lits {
		typ := info.TypeOf(expr)
		if typ == nil {
			continue
		}

		// Find reference to the type declaration of the struct being initialized.
		typ = typeparams.Deref(typ)
		tStruct, ok := typeparams.CoreType(typ).(*types.Struct)
		if !ok {
			continue
		}

		// Inv: typ is the possibly-named struct type.

		// fillableFields returns the number of fields in the struct that are
		// accessible and thus can be filled in the current package.
		fillableFields := func(t *types.Struct) (count int) {
			for field := range t.Fields() {
				if field.Pkg() == pkg || field.Exported() {
					count++
				}
			}
			return count
		}

		// fieldCount tracks the maximum number of fillable fields under the minimum
		// required expansion of embedded structs.
		//
		// It starts as the number of direct fields of the struct.
		//
		// Example structure:
		// A
		// ├── A1
		// ├── A2
		// └── B (embedded)
		//     ├── B1 (promoted to A)
		//     ├── B2
		//     └── C (embedded)
		//         ├── C1
		//         └── C2
		//
		// When a promoted field is filled (e.g., B1 in A{B1: 1}), we expand
		// fieldCount to include the fields of that embedded struct (B1, B2, C),
		// while removing the embedded struct B itself from the count.
		//
		// This means we need to fill all fields at B's level (B1, B2, C) and
		// its parent levels (A1, A2), but we don't need to fill B anymore. C
		// will be filled as C: C{} unless its own fields are accessed.
		fieldCount := fillableFields(tStruct)

		var seen map[*types.Var]bool

	nextElem:
		for _, el := range expr.Elts {
			kv, ok := el.(*ast.KeyValueExpr)
			if !ok {
				continue nextComp
			}

			key, ok := kv.Key.(*ast.Ident)
			if !ok {
				continue
			}

			seln, ok := types.LookupSelection(typ, true, pkg, key.Name)
			if !ok {
				continue
			}

			length := len(seln.Index())
			if length == 0 {
				continue
			}

			var (
				fields = make([]*types.Var, length-1)
				typs   = make([]*types.Struct, length-1)
			)

			i := 0
			for field := range typesinternal.ImplicitFieldSelections(seln) {
				t := field.Type()
				ptr, isPtr := t.Underlying().(*types.Pointer)
				if isPtr {
					t = ptr.Elem()
				}
				structType, ok := t.Underlying().(*types.Struct)
				if !ok {
					continue nextElem
				}

				fields[i] = field
				typs[i] = structType
				i++
			}

			for i, field := range fields {
				if !seen[field] {
					if seen == nil {
						seen = make(map[*types.Var]bool)
					}
					seen[field] = true
					fieldCount += fillableFields(typs[i]) - 1
				}
			}
		}

		// Skip any struct that is already populated or that has no fillable fields.
		if fieldCount == 0 || fieldCount == len(expr.Elts) {
			continue
		}

		// Derive a name for the struct type.
		var name string
		if typ != tStruct {
			// named struct type (e.g. pkg.S[T])
			name = types.TypeString(typ, typesinternal.NameRelativeTo(pkg))
		} else {
			// anonymous struct type
			var buf strings.Builder
			buf.WriteString("anonymous struct{ ")

			var printedCount int

			for field := range tStruct.Fields() {
				if field.Pkg() == pkg || field.Exported() {
					if buf.Len() > 38 || printedCount > 3 {
						buf.WriteString("...")
						break
					}
					fmt.Fprintf(&buf, "%s: %s; ", field.Name(), field.Type().String())
					printedCount++
				}
			}

			buf.WriteString(" }")
			name = buf.String()
		}

		diags = append(diags, analysis.Diagnostic{
			Message:  fmt.Sprintf("%s literal has missing fields", name),
			Pos:      expr.Pos(),
			End:      expr.End(),
			Category: FixCategory,
			SuggestedFixes: []analysis.SuggestedFix{{
				Message: fmt.Sprintf("Fill %s", name),
				// No TextEdits => computed later by gopls.
			}},
		})
	}

	return diags
}

const FixCategory = "fillstruct" // recognized by gopls ApplyFix

// SuggestedFix computes the suggested fix for the kinds of
// diagnostics produced by the Analyzer above.
func SuggestedFix(cpkg *cache.Package, pgf *parsego.File, start, end token.Pos) (*token.FileSet, *analysis.SuggestedFix, error) {
	var (
		file = pgf.File
		fset = cpkg.FileSet()
		pkg  = cpkg.Types()
		info = cpkg.TypesInfo()
		pos  = start // don't use end
	)
	cur, ok := pgf.Cursor().FindByPos(pos, pos)
	if !ok {
		return nil, nil, fmt.Errorf("no enclosing ast.Node")
	}
	expr, _ := cursorutil.FirstEnclosing[*ast.CompositeLit](cur)

	// newElts accumulates the newly generated field element AST nodes.
	newElts, err := populateMissingFields(info, pkg, file, expr, pos)
	if err != nil {
		return nil, nil, err
	}

	// If we failed to generate any new fields to fill, we have nothing to do.
	if len(newElts) == 0 {
		return nil, nil, fmt.Errorf("no elements to fill")
	}

	// Find the line on which the composite literal is declared.
	split := bytes.Split(pgf.Src, []byte("\n"))
	lineNumber := safetoken.StartPosition(fset, expr.Lbrace).Line
	firstLine := split[lineNumber-1] // lines are 1-indexed

	// Trim the whitespace from the left of the line, and use the index
	// to get the amount of whitespace on the left.
	trimmed := bytes.TrimLeftFunc(firstLine, unicode.IsSpace)
	index := bytes.Index(firstLine, trimmed)
	whitespace := firstLine[:index]

	var buf bytes.Buffer
	buf.WriteString("_{\n")
	fcmap := ast.NewCommentMap(fset, file, file.Comments)
	comments := fcmap.Filter(expr).Comments() // comments inside the expr, in source order
	for _, elt := range slices.Concat(expr.Elts, newElts) {
		// Print comments before the current elt
		for len(comments) > 0 && comments[0].Pos() < elt.Pos() {
			for _, co := range comments[0].List {
				fmt.Fprintln(&buf, co.Text)
			}
			comments = comments[1:]
		}

		// Print the current elt with comments
		eltcomments := fcmap.Filter(elt).Comments()
		if err := format.Node(&buf, fset, &printer.CommentedNode{Node: elt, Comments: eltcomments}); err != nil {
			return nil, nil, err
		}
		buf.WriteString(",")

		// Prune comments up to the end of the elt
		for len(comments) > 0 && comments[0].Pos() < elt.End() {
			comments = comments[1:]
		}

		// Write comments associated with the current elt that appear after it
		// printer.CommentedNode only prints comments inside the elt.
		for _, cg := range eltcomments {
			for _, co := range cg.List {
				if co.Pos() >= elt.End() {
					fmt.Fprintln(&buf, co.Text)
					if len(comments) > 0 {
						comments = comments[1:]
					}
				}
			}
		}
		buf.WriteString("\n")
	}
	buf.WriteString("}")
	formatted, err := format.Source(buf.Bytes())
	if err != nil {
		return nil, nil, err
	}

	sug := indent(formatted, whitespace)
	// Remove _
	idx := bytes.IndexByte(sug, '{') // cannot fail
	sug = sug[idx:]

	return fset, &analysis.SuggestedFix{
		TextEdits: []analysis.TextEdit{
			{
				Pos:     expr.Lbrace,
				End:     expr.Rbrace + token.Pos(len("}")),
				NewText: sug,
			},
		},
	}, nil
}

// populateMissingFields returns a slice of ast.Expr (specifically *ast.KeyValueExpr)
// representing the populated missing fields of tStruct that can be filled.
//
// It traverses the struct fields in depth-first order (DFS), flattening embedded
// structs where the user has partially initialized their sub-fields, and attempts
// to generate a matching local variable or zero-value for each missing field.
// Fields that cannot be populated are skipped, returning only the ones that can
// be successfully populated.
func populateMissingFields(info *types.Info, pkg *types.Package, file *ast.File, expr *ast.CompositeLit, pos token.Pos) ([]ast.Expr, error) {
	typ := info.TypeOf(expr)
	if typ == nil {
		return nil, fmt.Errorf("no composite literal")
	}

	// Find reference to the type declaration of the struct being initialized.
	typ = typeparams.Deref(typ)
	tStruct, ok := typ.Underlying().(*types.Struct)
	if !ok {
		return nil, fmt.Errorf("%s is not a (pointer to) struct type",
			types.TypeString(typ, typesinternal.NameRelativeTo(pkg)))
	}

	// Inv: typ is the possibly-named struct type.

	// explicit records whether each encountered field is explicitly initialized.
	// Each non-last field in a selection path is accessed implicitly (false),
	// and the last field is accessed explicitly (true).
	// Fields not present in this map are completely missing and need to be populated.
	explicit := make(map[*types.Var]bool)
	for _, elem := range expr.Elts {
		kv, ok := elem.(*ast.KeyValueExpr)
		if !ok {
			return nil, fmt.Errorf("cannot fill struct literal containing unkeyed elements")
		}

		key, ok := kv.Key.(*ast.Ident)
		if !ok {
			continue
		}

		seln, ok := types.LookupSelection(tStruct, true, pkg, key.Name)
		if !ok {
			continue
		}

		field, ok := seln.Obj().(*types.Var)
		if !ok {
			continue
		}

		isExplicit, ok := explicit[field]
		if ok && !isExplicit {
			return nil, fmt.Errorf("cannot fill both %q and its subfields", field.Name())
		}
		explicit[field] = true // last field is explicit

		for field := range typesinternal.ImplicitFieldSelections(seln) {
			if explicit[field] {
				return nil, fmt.Errorf("cannot fill both %q and its subfields", field.Name())
			}
			explicit[field] = false // all the others are implicit
		}
	}

	// Collect the final list of fields to be filled. Traverse the struct fields
	// in depth-first order, flattening embedded fields that are marked for
	// expansion because the user has partially initialized their sub-fields.
	var fields []*types.Var
	{
		var addFields func(*types.Struct)
		addFields = func(tStruct *types.Struct) {
			for field := range tStruct.Fields() {
				if field.Pkg() != pkg && !field.Exported() {
					continue
				}

				isExplicit, ok := explicit[field]
				if !ok {
					fields = append(fields, field)
					continue
				}

				if !isExplicit {
					tInner, ok := typeparams.Deref(field.Type()).Underlying().(*types.Struct)
					if !ok {
						continue // can't happen
					}
					addFields(tInner)
				}
			}
		}
		addFields(tStruct)
	}

	typs := make([]types.Type, len(fields))
	for i, f := range fields {
		typs[i] = f.Type()
	}

	var newElts []ast.Expr
	matches := fillreturns.MatchingIdents(typs, file, pos, info, pkg)
	qual := typesinternal.FileQualifier(file, pkg)

	// Iterate in the order fields were discovered to ensure deterministic
	// output and match the struct definition order.
	for _, field := range fields {
		// TODO(hxjiang): Provide a separate quick-fix option to fill the
		// struct using nested composite literals, ensuring we always have a
		// valid suggestion even if shadowing prevents flattening.
		if obj, _, _ := types.LookupFieldOrMethod(tStruct, true, pkg, field.Name()); obj != field {
			return nil, fmt.Errorf("field %s shadowed", field.Name())
		}

		kv := &ast.KeyValueExpr{
			Key: ast.NewIdent(field.Name()),
		}

		names, ok := matches[field.Type()]
		if !ok {
			return nil, fmt.Errorf("invalid struct field type: %v", field.Type())
		}

		// Find the name most similar to the field name.
		// If no name matches the pattern, generate a zero value.
		// NOTE: We currently match on the name of the field key rather than the field type.
		if best := fuzzy.BestMatch(field.Name(), names); best != "" {
			kv.Value = ast.NewIdent(best)
		} else if expr, isValid := populateValue(field.Type(), qual); isValid {
			kv.Value = expr
		} else {
			continue
		}

		newElts = append(newElts, kv)
	}
	return newElts, nil
}

// indent works line by line through str, indenting (prefixing) each line with
// ind.
func indent(str, ind []byte) []byte {
	split := bytes.Split(str, []byte("\n"))
	newText := bytes.NewBuffer(nil)
	for i, s := range split {
		if len(s) == 0 {
			continue
		}
		// Don't add the extra indentation to the first line.
		if i != 0 {
			newText.Write(ind)
		}
		newText.Write(s)
		if i < len(split)-1 {
			newText.WriteByte('\n')
		}
	}
	return newText.Bytes()
}

// populateValue constructs an expression to fill the value of a struct field.
//
// When the type of a struct field is a basic literal or interface, we return
// default values. For other types, such as maps, slices, and channels, we create
// empty expressions such as []T{} or make(chan T) rather than using default values.
//
// The reasoning here is that users will call fillstruct with the intention of
// initializing the struct, in which case setting these fields to nil has no effect.
//
// If the input contains an invalid type, populateValue may panic or return
// expression that may not compile.
func populateValue(typ types.Type, qual types.Qualifier) (_ ast.Expr, isValid bool) {
	switch t := typ.(type) {
	case *types.TypeParam, *types.Interface, *types.Struct, *types.Basic:
		return typesinternal.ZeroExpr(t, qual)

	case *types.Alias, *types.Named:
		switch t.Underlying().(type) {
		// Avoid typesinternal.ZeroExpr here as we don't want to return nil.
		case *types.Map, *types.Slice:
			return &ast.CompositeLit{
				Type: typesinternal.TypeExpr(t, qual),
			}, true
		default:
			return typesinternal.ZeroExpr(t, qual)
		}

	// Avoid typesinternal.ZeroExpr here as we don't want to return nil.
	case *types.Map, *types.Slice:
		return &ast.CompositeLit{
			Type: typesinternal.TypeExpr(t, qual),
		}, true

	case *types.Array:
		return &ast.CompositeLit{
			Type: &ast.ArrayType{
				Elt: typesinternal.TypeExpr(t.Elem(), qual),
				Len: &ast.BasicLit{
					Kind: token.INT, Value: fmt.Sprintf("%v", t.Len()),
				},
			},
		}, true

	case *types.Chan:
		dir := ast.ChanDir(t.Dir())
		if t.Dir() == types.SendRecv {
			dir = ast.SEND | ast.RECV
		}
		return &ast.CallExpr{
			Fun: ast.NewIdent("make"),
			Args: []ast.Expr{
				&ast.ChanType{
					Dir:   dir,
					Value: typesinternal.TypeExpr(t.Elem(), qual),
				},
			},
		}, true

	case *types.Signature:
		return &ast.FuncLit{
			Type: typesinternal.TypeExpr(t, qual).(*ast.FuncType),
			// The body of the function literal contains a panic statement to
			// avoid type errors.
			Body: &ast.BlockStmt{
				List: []ast.Stmt{
					&ast.ExprStmt{
						X: &ast.CallExpr{
							Fun: ast.NewIdent("panic"),
							Args: []ast.Expr{
								&ast.BasicLit{
									Kind:  token.STRING,
									Value: `"TODO"`,
								},
							},
						},
					},
				},
			},
		}, true

	case *types.Pointer:
		switch tt := types.Unalias(t.Elem()).(type) {
		case *types.Basic:
			return &ast.CallExpr{
				Fun: &ast.Ident{
					Name: "new",
				},
				Args: []ast.Expr{
					&ast.Ident{
						Name: t.Elem().String(),
					},
				},
			}, true
		// Pointer to type parameter should return new(T) instead of &*new(T).
		case *types.TypeParam:
			return &ast.CallExpr{
				Fun: &ast.Ident{
					Name: "new",
				},
				Args: []ast.Expr{
					&ast.Ident{
						Name: tt.Obj().Name(),
					},
				},
			}, true
		default:
			// TODO(hxjiang): & prefix only works if populateValue returns a
			// composite literal T{} or the expression new(T).
			expr, isValid := populateValue(t.Elem(), qual)
			return &ast.UnaryExpr{
				Op: token.AND,
				X:  expr,
			}, isValid
		}
	}
	return nil, false
}
