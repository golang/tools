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
	"strings"
	"unicode"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/ast/astutil"
	"golang.org/x/tools/gopls/internal/fuzzy"
	"golang.org/x/tools/gopls/internal/util/safetoken"
	"golang.org/x/tools/internal/analysisinternal"
	"golang.org/x/tools/internal/typeparams"
	"golang.org/x/tools/internal/typesinternal"
)

// Diagnose computes diagnostics for fillable struct literals overlapping with
// the provided start and end position of file f.
//
// The diagnostic contains a lazy fix; the actual patch is computed
// (via the ApplyFix command) by a call to [SuggestedFix].
//
// If either start or end is invalid, the entire file is inspected.
func Diagnose(f *ast.File, start, end token.Pos, pkg *types.Package, info *types.Info) []analysis.Diagnostic {
	var diags []analysis.Diagnostic
	ast.Inspect(f, func(n ast.Node) bool {
		if n == nil {
			return true // pop
		}
		if start.IsValid() && n.End() < start || end.IsValid() && n.Pos() > end {
			return false // skip non-overlapping subtree
		}
		expr, ok := n.(*ast.CompositeLit)
		if !ok {
			return true
		}
		typ := info.TypeOf(expr)
		if typ == nil {
			return true
		}

		// Find reference to the type declaration of the struct being initialized.
		typ = typeparams.Deref(typ)
		tStruct, ok := typeparams.CoreType(typ).(*types.Struct)
		if !ok {
			return true
		}
		// Inv: typ is the possibly-named struct type.

		fieldCount := tStruct.NumFields()

		// Skip any struct that is already populated or that has no fields.
		if fieldCount == 0 || fieldCount == len(expr.Elts) {
			return true
		}

		// Are any fields in need of filling?
		var fillableFields []string
		for i := 0; i < fieldCount; i++ {
			field := tStruct.Field(i)
			// Ignore fields that are not accessible in the current package.
			if field.Pkg() != nil && field.Pkg() != pkg && !field.Exported() {
				continue
			}
			fillableFields = append(fillableFields, fmt.Sprintf("%s: %s", field.Name(), field.Type().String()))
		}
		if len(fillableFields) == 0 {
			return true
		}

		// Derive a name for the struct type.
		var name string
		if typ != tStruct {
			// named struct type (e.g. pkg.S[T])
			name = types.TypeString(typ, typesinternal.NameRelativeTo(pkg))
		} else {
			// anonymous struct type
			totalFields := len(fillableFields)
			const maxLen = 20
			// Find the index to cut off printing of fields.
			var i, fieldLen int
			for i = range fillableFields {
				if fieldLen > maxLen {
					break
				}
				fieldLen += len(fillableFields[i])
			}
			fillableFields = fillableFields[:i]
			if i < totalFields {
				fillableFields = append(fillableFields, "...")
			}
			name = fmt.Sprintf("anonymous struct{ %s }", strings.Join(fillableFields, ", "))
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
		return true
	})

	return diags
}

const FixCategory = "fillstruct" // recognized by gopls ApplyFix

// SuggestedFix computes the suggested fix for the kinds of
// diagnostics produced by the Analyzer above.
func SuggestedFix(fset *token.FileSet, start, end token.Pos, content []byte, file *ast.File, pkg *types.Package, info *types.Info) (*token.FileSet, *analysis.SuggestedFix, error) {
	if info == nil {
		return nil, nil, fmt.Errorf("nil types.Info")
	}

	pos := start // don't use the end

	// TODO(rstambler): Using ast.Inspect would probably be more efficient than
	// calling PathEnclosingInterval. Switch this approach.
	path, _ := astutil.PathEnclosingInterval(file, pos, pos)
	if len(path) == 0 {
		return nil, nil, fmt.Errorf("no enclosing ast.Node")
	}
	var expr *ast.CompositeLit
	for _, n := range path {
		if node, ok := n.(*ast.CompositeLit); ok {
			expr = node
			break
		}
	}

	typ := info.TypeOf(expr)
	if typ == nil {
		return nil, nil, fmt.Errorf("no composite literal")
	}

	// Find reference to the type declaration of the struct being initialized.
	typ = typeparams.Deref(typ)
	tStruct, ok := typ.Underlying().(*types.Struct)
	if !ok {
		return nil, nil, fmt.Errorf("%s is not a (pointer to) struct type",
			types.TypeString(typ, typesinternal.NameRelativeTo(pkg)))
	}
	// Inv: typ is the possibly-named struct type.

	fieldCount := tStruct.NumFields()

	// Check which types have already been filled in. (we only want to fill in
	// the unfilled types, or else we'll blat user-supplied details)
	prefilledFields := map[string]ast.Expr{}
	var elts []ast.Expr
	for _, e := range expr.Elts {
		if kv, ok := e.(*ast.KeyValueExpr); ok {
			if key, ok := kv.Key.(*ast.Ident); ok {
				prefilledFields[key.Name] = kv.Value
				elts = append(elts, kv)
			}
		}
	}

	var fieldTyps []types.Type
	for i := 0; i < fieldCount; i++ {
		field := tStruct.Field(i)
		// Ignore fields that are not accessible in the current package.
		if field.Pkg() != nil && field.Pkg() != pkg && !field.Exported() {
			fieldTyps = append(fieldTyps, nil)
			continue
		}
		fieldTyps = append(fieldTyps, field.Type())
	}
	matches := analysisinternal.MatchingIdents(fieldTyps, file, start, info, pkg)
	qual := typesinternal.FileQualifier(file, pkg)

	for i, fieldTyp := range fieldTyps {
		if fieldTyp == nil {
			continue // TODO(adonovan): is this reachable?
		}
		fieldName := tStruct.Field(i).Name()
		if _, ok := prefilledFields[fieldName]; ok {
			// We already stored these when looping over expr.Elt.
			// Want to preserve the original order of prefilled fields
			continue
		}

		kv := &ast.KeyValueExpr{
			Key: &ast.Ident{
				Name: fieldName,
			},
		}

		names, ok := matches[fieldTyp]
		if !ok {
			return nil, nil, fmt.Errorf("invalid struct field type: %v", fieldTyp)
		}

		// Find the name most similar to the field name.
		// If no name matches the pattern, generate a zero value.
		// NOTE: We currently match on the name of the field key rather than the field type.
		if best := fuzzy.BestMatch(fieldName, names); best != "" {
			kv.Value = ast.NewIdent(best)
		} else if expr, isValid := populateValue(fieldTyp, qual); isValid {
			kv.Value = expr
		} else {
			return nil, nil, nil // no fix to suggest
		}

		elts = append(elts, kv)
	}

	// If all of the struct's fields are unexported, we have nothing to do.
	if len(elts) == 0 {
		return nil, nil, fmt.Errorf("no elements to fill")
	}

	// Find the line on which the composite literal is declared.
	split := bytes.Split(content, []byte("\n"))
	lineNumber := safetoken.StartPosition(fset, expr.Lbrace).Line
	firstLine := split[lineNumber-1] // lines are 1-indexed

	// Trim the whitespace from the left of the line, and use the index
	// to get the amount of whitespace on the left.
	trimmed := bytes.TrimLeftFunc(firstLine, unicode.IsSpace)
	index := bytes.Index(firstLine, trimmed)
	whitespace := firstLine[:index]

	// Write a new composite literal "_{...}" composed of all prefilled and new elements,
	// preserving existing formatting and comments.
	// An alternative would be to only format the new fields,
	// but by printing the entire composite literal, we ensure
	// that the result is gofmt'ed.
	var buf bytes.Buffer
	buf.WriteString("_{\n")
	fcmap := ast.NewCommentMap(fset, file, file.Comments)
	comments := fcmap.Filter(expr).Comments() // comments inside the expr, in source order
	for _, elt := range elts {
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
