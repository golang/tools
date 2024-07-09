// Copyright 2022 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package golang

import (
	"context"
	"fmt"
	"go/ast"
	"go/constant"
	"go/token"
	"go/types"
	"strings"

	"golang.org/x/tools/gopls/internal/cache"
	"golang.org/x/tools/gopls/internal/file"
	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/gopls/internal/settings"
	"golang.org/x/tools/gopls/internal/util/typesutil"
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
	q := typesutil.FileQualifier(pgf.File, pkg.Types(), info)

	// Set the range to the full file if the range is not valid.
	start, end := pgf.File.Pos(), pgf.File.End()
	if pRng.Start.Line < pRng.End.Line || pRng.Start.Character < pRng.End.Character {
		// Adjust start and end for the specified range.
		var err error
		start, end, err = pgf.RangePos(pRng)
		if err != nil {
			return nil, err
		}
	}

	var hints []protocol.InlayHint
	ast.Inspect(pgf.File, func(node ast.Node) bool {
		// If not in range, we can stop looking.
		if node == nil || node.End() < start || node.Pos() > end {
			return false
		}
		for _, fn := range enabledHints {
			hints = append(hints, fn(node, pgf.Mapper, pgf.Tok, info, &q)...)
		}
		return true
	})
	return hints, nil
}

type inlayHintFunc func(node ast.Node, m *protocol.Mapper, tf *token.File, info *types.Info, q *types.Qualifier) []protocol.InlayHint

var allInlayHints = map[settings.InlayHint]inlayHintFunc{
	settings.AssignVariableTypes:        assignVariableTypes,
	settings.ConstantValues:             constantValues,
	settings.ParameterNames:             parameterNames,
	settings.RangeVariableTypes:         rangeVariableTypes,
	settings.CompositeLiteralTypes:      compositeLiteralTypes,
	settings.CompositeLiteralFieldNames: compositeLiteralFields,
	settings.FunctionTypeParameters:     funcTypeParams,
}

func parameterNames(node ast.Node, m *protocol.Mapper, tf *token.File, info *types.Info, _ *types.Qualifier) []protocol.InlayHint {
	callExpr, ok := node.(*ast.CallExpr)
	if !ok {
		return nil
	}
	t := info.TypeOf(callExpr.Fun)
	if t == nil {
		return nil
	}
	signature, ok := typeparams.CoreType(t).(*types.Signature)
	if !ok {
		return nil
	}

	var hints []protocol.InlayHint
	for i, v := range callExpr.Args {
		start, err := m.PosPosition(tf, v.Pos())
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
		hints = append(hints, protocol.InlayHint{
			Position:     start,
			Label:        buildLabel(label + ":"),
			Kind:         protocol.Parameter,
			PaddingRight: true,
		})
	}
	return hints
}

func funcTypeParams(node ast.Node, m *protocol.Mapper, tf *token.File, info *types.Info, _ *types.Qualifier) []protocol.InlayHint {
	ce, ok := node.(*ast.CallExpr)
	if !ok {
		return nil
	}
	id, ok := ce.Fun.(*ast.Ident)
	if !ok {
		return nil
	}
	inst := info.Instances[id]
	if inst.TypeArgs == nil {
		return nil
	}
	start, err := m.PosPosition(tf, id.End())
	if err != nil {
		return nil
	}
	var args []string
	for i := 0; i < inst.TypeArgs.Len(); i++ {
		args = append(args, inst.TypeArgs.At(i).String())
	}
	if len(args) == 0 {
		return nil
	}
	return []protocol.InlayHint{{
		Position: start,
		Label:    buildLabel("[" + strings.Join(args, ", ") + "]"),
		Kind:     protocol.Type,
	}}
}

func assignVariableTypes(node ast.Node, m *protocol.Mapper, tf *token.File, info *types.Info, q *types.Qualifier) []protocol.InlayHint {
	stmt, ok := node.(*ast.AssignStmt)
	if !ok || stmt.Tok != token.DEFINE {
		return nil
	}

	var hints []protocol.InlayHint
	for _, v := range stmt.Lhs {
		if h := variableType(v, m, tf, info, q); h != nil {
			hints = append(hints, *h)
		}
	}
	return hints
}

func rangeVariableTypes(node ast.Node, m *protocol.Mapper, tf *token.File, info *types.Info, q *types.Qualifier) []protocol.InlayHint {
	rStmt, ok := node.(*ast.RangeStmt)
	if !ok {
		return nil
	}
	var hints []protocol.InlayHint
	if h := variableType(rStmt.Key, m, tf, info, q); h != nil {
		hints = append(hints, *h)
	}
	if h := variableType(rStmt.Value, m, tf, info, q); h != nil {
		hints = append(hints, *h)
	}
	return hints
}

func variableType(e ast.Expr, m *protocol.Mapper, tf *token.File, info *types.Info, q *types.Qualifier) *protocol.InlayHint {
	typ := info.TypeOf(e)
	if typ == nil {
		return nil
	}
	end, err := m.PosPosition(tf, e.End())
	if err != nil {
		return nil
	}
	return &protocol.InlayHint{
		Position:    end,
		Label:       buildLabel(types.TypeString(typ, *q)),
		Kind:        protocol.Type,
		PaddingLeft: true,
	}
}

func constantValues(node ast.Node, m *protocol.Mapper, tf *token.File, info *types.Info, _ *types.Qualifier) []protocol.InlayHint {
	genDecl, ok := node.(*ast.GenDecl)
	if !ok || genDecl.Tok != token.CONST {
		return nil
	}

	var hints []protocol.InlayHint
	for _, v := range genDecl.Specs {
		spec, ok := v.(*ast.ValueSpec)
		if !ok {
			continue
		}
		end, err := m.PosPosition(tf, v.End())
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
				return nil
			}
			if checkValues {
				switch spec.Values[i].(type) {
				case *ast.BadExpr:
					return nil
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
		hints = append(hints, protocol.InlayHint{
			Position:    end,
			Label:       buildLabel("= " + strings.Join(values, ", ")),
			PaddingLeft: true,
		})
	}
	return hints
}

func compositeLiteralFields(node ast.Node, m *protocol.Mapper, tf *token.File, info *types.Info, _ *types.Qualifier) []protocol.InlayHint {
	compLit, ok := node.(*ast.CompositeLit)
	if !ok {
		return nil
	}
	typ := info.TypeOf(compLit)
	if typ == nil {
		return nil
	}
	typ = typesinternal.Unpointer(typ)
	strct, ok := typeparams.CoreType(typ).(*types.Struct)
	if !ok {
		return nil
	}

	var hints []protocol.InlayHint
	var allEdits []protocol.TextEdit
	for i, v := range compLit.Elts {
		if _, ok := v.(*ast.KeyValueExpr); !ok {
			start, err := m.PosPosition(tf, v.Pos())
			if err != nil {
				continue
			}
			if i > strct.NumFields()-1 {
				break
			}
			hints = append(hints, protocol.InlayHint{
				Position:     start,
				Label:        buildLabel(strct.Field(i).Name() + ":"),
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
	}
	return hints
}

func compositeLiteralTypes(node ast.Node, m *protocol.Mapper, tf *token.File, info *types.Info, q *types.Qualifier) []protocol.InlayHint {
	compLit, ok := node.(*ast.CompositeLit)
	if !ok {
		return nil
	}
	typ := info.TypeOf(compLit)
	if typ == nil {
		return nil
	}
	if compLit.Type != nil {
		return nil
	}
	prefix := ""
	if t, ok := typeparams.CoreType(typ).(*types.Pointer); ok {
		typ = t.Elem()
		prefix = "&"
	}
	// The type for this composite literal is implicit, add an inlay hint.
	start, err := m.PosPosition(tf, compLit.Lbrace)
	if err != nil {
		return nil
	}
	return []protocol.InlayHint{{
		Position: start,
		Label:    buildLabel(fmt.Sprintf("%s%s", prefix, types.TypeString(typ, *q))),
		Kind:     protocol.Type,
	}}
}

func buildLabel(s string) []protocol.InlayHintLabelPart {
	const maxLabelLength = 28
	label := protocol.InlayHintLabelPart{
		Value: s,
	}
	if len(s) > maxLabelLength+len("...") {
		label.Value = s[:maxLabelLength] + "..."
	}
	return []protocol.InlayHintLabelPart{label}
}
