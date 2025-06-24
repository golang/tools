// Copyright 2022 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package golang

import (
	"cmp"
	"context"
	"fmt"
	"go/ast"
	"go/constant"
	"go/token"
	"go/types"
	"slices"
	"strings"

	"golang.org/x/tools/go/ast/inspector"
	"golang.org/x/tools/go/types/typeutil"
	"golang.org/x/tools/gopls/internal/cache"
	"golang.org/x/tools/gopls/internal/cache/parsego"
	"golang.org/x/tools/gopls/internal/file"
	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/gopls/internal/settings"
	"golang.org/x/tools/internal/analysisinternal"
	"golang.org/x/tools/internal/event"
	"golang.org/x/tools/internal/typeparams"
	"golang.org/x/tools/internal/typesinternal"
)

func InlayHint(ctx context.Context, snapshot *cache.Snapshot, fh file.Handle, pRng protocol.Range) ([]protocol.InlayHint, error) {
	ctx, done := event.Start(ctx, "golang.InlayHint")
	defer done()

	pkg, pgf, err := NarrowestPackageForFile(ctx, snapshot, fh.URI())
	if err != nil {
		return nil, fmt.Errorf("getting file for InlayHint: %w", err)
	}

	// Collect a list of the inlay hints that are enabled.
	inlayHintOptions := snapshot.Options().InlayHintOptions
	var enabledHints []inlayHintFunc
	for hint, enabled := range inlayHintOptions.Hints {
		if !enabled {
			continue
		}
		if fn, ok := allInlayHints[hint]; ok {
			enabledHints = append(enabledHints, fn)
		}
	}
	if len(enabledHints) == 0 {
		return nil, nil
	}

	info := pkg.TypesInfo()
	qual := typesinternal.FileQualifier(pgf.File, pkg.Types())

	// Set the range to the full file if the range is not valid.
	start, end := pgf.File.FileStart, pgf.File.FileEnd

	// TODO(adonovan): this condition looks completely wrong!
	if pRng.Start.Line < pRng.End.Line || pRng.Start.Character < pRng.End.Character {
		// Adjust start and end for the specified range.
		var err error
		start, end, err = pgf.RangePos(pRng)
		if err != nil {
			return nil, err
		}
	}

	var hints []protocol.InlayHint
	if curSubrange, ok := pgf.Cursor.FindByPos(start, end); ok {
		add := func(hint protocol.InlayHint) { hints = append(hints, hint) }
		for _, fn := range enabledHints {
			fn(info, pgf, qual, curSubrange, add)
		}
	}
	return hints, nil
}

type inlayHintFunc func(info *types.Info, pgf *parsego.File, qual types.Qualifier, cur inspector.Cursor, add func(protocol.InlayHint))

var allInlayHints = map[settings.InlayHint]inlayHintFunc{
	settings.AssignVariableTypes:        assignVariableTypes,
	settings.ConstantValues:             constantValues,
	settings.ParameterNames:             parameterNames,
	settings.RangeVariableTypes:         rangeVariableTypes,
	settings.CompositeLiteralTypes:      compositeLiteralTypes,
	settings.CompositeLiteralFieldNames: compositeLiteralFields,
	settings.FunctionTypeParameters:     funcTypeParams,
	settings.IgnoredError:               ignoredError,
}

func parameterNames(info *types.Info, pgf *parsego.File, qual types.Qualifier, cur inspector.Cursor, add func(protocol.InlayHint)) {
	for curCall := range cur.Preorder((*ast.CallExpr)(nil)) {
		callExpr := curCall.Node().(*ast.CallExpr)
		t := info.TypeOf(callExpr.Fun)
		if t == nil {
			continue
		}
		signature, ok := typeparams.CoreType(t).(*types.Signature)
		if !ok {
			continue
		}

		for i, v := range callExpr.Args {
			start, err := pgf.PosPosition(v.Pos())
			if err != nil {
				continue
			}
			params := signature.Params()
			// When a function has variadic params, we skip args after
			// params.Len().
			if i > params.Len()-1 {
				break
			}
			param := params.At(i)
			// param.Name is empty for built-ins like append
			if param.Name() == "" {
				continue
			}
			// Skip the parameter name hint if the arg matches
			// the parameter name.
			if i, ok := v.(*ast.Ident); ok && i.Name == param.Name() {
				continue
			}

			label := param.Name()
			if signature.Variadic() && i == params.Len()-1 {
				label = label + "..."
			}
			add(protocol.InlayHint{
				Position:     start,
				Label:        labelPart(label + ":"),
				Kind:         protocol.Parameter,
				PaddingRight: true,
			})
		}
	}
}

func ignoredError(info *types.Info, pgf *parsego.File, qual types.Qualifier, cur inspector.Cursor, add func(protocol.InlayHint)) {
outer:
	for curCall := range cur.Preorder((*ast.ExprStmt)(nil)) {
		stmt := curCall.Node().(*ast.ExprStmt)
		call, ok := stmt.X.(*ast.CallExpr)
		if !ok {
			continue // not a call stmt
		}

		// Check that type of result (or last component) is error.
		tv, ok := info.Types[call]
		if !ok {
			continue // no type info
		}
		t := tv.Type
		if res, ok := t.(*types.Tuple); ok && res.Len() > 1 {
			t = res.At(res.Len() - 1).Type()
		}
		if !types.Identical(t, errorType) {
			continue
		}

		// Suppress some common false positives.
		obj := typeutil.Callee(info, call)
		if analysisinternal.IsFunctionNamed(obj, "fmt", "Print", "Printf", "Println", "Fprint", "Fprintf", "Fprintln") ||
			analysisinternal.IsMethodNamed(obj, "bytes", "Buffer", "Write", "WriteByte", "WriteRune", "WriteString") ||
			analysisinternal.IsMethodNamed(obj, "strings", "Builder", "Write", "WriteByte", "WriteRune", "WriteString") {
			continue
		}

		// Suppress if closely following comment contains "// ignore error".
		comments := pgf.File.Comments
		compare := func(cg *ast.CommentGroup, pos token.Pos) int {
			return cmp.Compare(cg.Pos(), pos)
		}
		i, _ := slices.BinarySearchFunc(comments, stmt.End(), compare)
		if i >= 0 && i < len(comments) {
			cg := comments[i]
			if cg.Pos() < stmt.End()+3 && strings.Contains(cg.Text(), "ignore error") {
				continue outer // suppress
			}
		}

		// Provide a hint.
		pos, err := pgf.PosPosition(stmt.End())
		if err != nil {
			continue
		}
		add(protocol.InlayHint{
			Position: pos,
			Label:    labelPart(" // ignore error"),
		})
	}
}

func funcTypeParams(info *types.Info, pgf *parsego.File, qual types.Qualifier, cur inspector.Cursor, add func(protocol.InlayHint)) {
	for curCall := range cur.Preorder((*ast.CallExpr)(nil)) {
		call := curCall.Node().(*ast.CallExpr)
		id, ok := call.Fun.(*ast.Ident)
		if !ok {
			continue
		}
		inst := info.Instances[id]
		if inst.TypeArgs == nil {
			continue
		}
		start, err := pgf.PosPosition(id.End())
		if err != nil {
			continue
		}
		var args []string
		for i := 0; i < inst.TypeArgs.Len(); i++ {
			args = append(args, inst.TypeArgs.At(i).String())
		}
		if len(args) == 0 {
			continue
		}
		add(protocol.InlayHint{
			Position: start,
			Label:    labelPart("[" + strings.Join(args, ", ") + "]"),
			Kind:     protocol.Type,
		})
	}
}

func assignVariableTypes(info *types.Info, pgf *parsego.File, qual types.Qualifier, cur inspector.Cursor, add func(protocol.InlayHint)) {
	for node := range cur.Preorder((*ast.AssignStmt)(nil), (*ast.ValueSpec)(nil)) {
		switch n := node.Node().(type) {
		case *ast.AssignStmt:
			if n.Tok != token.DEFINE {
				continue
			}
			for _, v := range n.Lhs {
				variableType(info, pgf, qual, v, add)
			}
		case *ast.GenDecl:
			if n.Tok != token.VAR {
				continue
			}
			for _, v := range n.Specs {
				spec := v.(*ast.ValueSpec)
				// The type of the variable is written, skip showing type of this var.
				// ```go
				// var foo string
				// ```
				if spec.Type != nil {
					continue
				}

				for _, v := range spec.Names {
					variableType(info, pgf, qual, v, add)
				}
			}
		}
	}
}

func rangeVariableTypes(info *types.Info, pgf *parsego.File, qual types.Qualifier, cur inspector.Cursor, add func(protocol.InlayHint)) {
	for curRange := range cur.Preorder((*ast.RangeStmt)(nil)) {
		rStmt := curRange.Node().(*ast.RangeStmt)
		variableType(info, pgf, qual, rStmt.Key, add)
		variableType(info, pgf, qual, rStmt.Value, add)
	}
}

func variableType(info *types.Info, pgf *parsego.File, qual types.Qualifier, e ast.Expr, add func(protocol.InlayHint)) {
	typ := info.TypeOf(e)
	if typ == nil {
		return
	}
	end, err := pgf.PosPosition(e.End())
	if err != nil {
		return
	}
	add(protocol.InlayHint{
		Position:    end,
		Label:       labelPart(types.TypeString(typ, qual)),
		Kind:        protocol.Type,
		PaddingLeft: true,
	})
}

func constantValues(info *types.Info, pgf *parsego.File, qual types.Qualifier, cur inspector.Cursor, add func(protocol.InlayHint)) {
	for curDecl := range cur.Preorder((*ast.GenDecl)(nil)) {
		genDecl := curDecl.Node().(*ast.GenDecl)
		if genDecl.Tok != token.CONST {
			continue
		}

		for _, v := range genDecl.Specs {
			spec, ok := v.(*ast.ValueSpec)
			if !ok {
				continue
			}
			end, err := pgf.PosPosition(v.End())
			if err != nil {
				continue
			}
			// Show hints when values are missing or at least one value is not
			// a basic literal.
			showHints := len(spec.Values) == 0
			checkValues := len(spec.Names) == len(spec.Values)
			var values []string
			for i, w := range spec.Names {
				obj, ok := info.ObjectOf(w).(*types.Const)
				if !ok || obj.Val().Kind() == constant.Unknown {
					continue
				}
				if checkValues {
					switch spec.Values[i].(type) {
					case *ast.BadExpr:
						continue
					case *ast.BasicLit:
					default:
						if obj.Val().Kind() != constant.Bool {
							showHints = true
						}
					}
				}
				values = append(values, fmt.Sprintf("%v", obj.Val()))
			}
			if !showHints || len(values) == 0 {
				continue
			}
			add(protocol.InlayHint{
				Position:    end,
				Label:       labelPart("= " + strings.Join(values, ", ")),
				PaddingLeft: true,
			})
		}
	}
}

func compositeLiteralFields(info *types.Info, pgf *parsego.File, qual types.Qualifier, cur inspector.Cursor, add func(protocol.InlayHint)) {
	for curCompLit := range cur.Preorder((*ast.CompositeLit)(nil)) {
		compLit, ok := curCompLit.Node().(*ast.CompositeLit)
		if !ok {
			continue
		}
		typ := info.TypeOf(compLit)
		if typ == nil {
			continue
		}
		typ = typesinternal.Unpointer(typ)
		strct, ok := typeparams.CoreType(typ).(*types.Struct)
		if !ok {
			continue
		}

		var hints []protocol.InlayHint
		var allEdits []protocol.TextEdit
		for i, v := range compLit.Elts {
			if _, ok := v.(*ast.KeyValueExpr); !ok {
				start, err := pgf.PosPosition(v.Pos())
				if err != nil {
					continue
				}
				if i > strct.NumFields()-1 {
					break
				}
				hints = append(hints, protocol.InlayHint{
					Position:     start,
					Label:        labelPart(strct.Field(i).Name() + ":"),
					Kind:         protocol.Parameter,
					PaddingRight: true,
				})
				allEdits = append(allEdits, protocol.TextEdit{
					Range:   protocol.Range{Start: start, End: start},
					NewText: strct.Field(i).Name() + ": ",
				})
			}
		}
		// It is not allowed to have a mix of keyed and unkeyed fields, so
		// have the text edits add keys to all fields.
		for i := range hints {
			hints[i].TextEdits = allEdits
			add(hints[i])
		}
	}
}

func compositeLiteralTypes(info *types.Info, pgf *parsego.File, qual types.Qualifier, cur inspector.Cursor, add func(protocol.InlayHint)) {
	for curCompLit := range cur.Preorder((*ast.CompositeLit)(nil)) {
		compLit := curCompLit.Node().(*ast.CompositeLit)
		typ := info.TypeOf(compLit)
		if typ == nil {
			continue
		}
		if compLit.Type != nil {
			continue
		}
		prefix := ""
		if t, ok := typeparams.CoreType(typ).(*types.Pointer); ok {
			typ = t.Elem()
			prefix = "&"
		}
		// The type for this composite literal is implicit, add an inlay hint.
		start, err := pgf.PosPosition(compLit.Lbrace)
		if err != nil {
			continue
		}
		add(protocol.InlayHint{
			Position: start,
			Label:    labelPart(fmt.Sprintf("%s%s", prefix, types.TypeString(typ, qual))),
			Kind:     protocol.Type,
		})
	}
}

func labelPart(s string) []protocol.InlayHintLabelPart {
	const maxLabelLength = 28
	if len(s) > maxLabelLength+len("...") {
		s = s[:maxLabelLength] + "..."
	}
	return []protocol.InlayHintLabelPart{{Value: s}}
}
