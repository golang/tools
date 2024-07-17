// Copyright 2019 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package completion

import (
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/doc"
	"go/types"
	"strings"

	"golang.org/x/tools/gopls/internal/golang"
	"golang.org/x/tools/gopls/internal/golang/completion/snippet"
	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/gopls/internal/util/safetoken"
	"golang.org/x/tools/internal/aliases"
	"golang.org/x/tools/internal/event"
	"golang.org/x/tools/internal/imports"
)

var (
	errNoMatch  = errors.New("not a surrounding match")
	errLowScore = errors.New("not a high scoring candidate")
)

// item formats a candidate to a CompletionItem.
func (c *completer) item(ctx context.Context, cand candidate) (CompletionItem, error) {
	obj := cand.obj

	// if the object isn't a valid match against the surrounding, return early.
	matchScore := c.matcher.Score(cand.name)
	if matchScore <= 0 {
		return CompletionItem{}, errNoMatch
	}
	cand.score *= float64(matchScore)

	// Ignore deep candidates that won't be in the MaxDeepCompletions anyway.
	if len(cand.path) != 0 && !c.deepState.isHighScore(cand.score) {
		return CompletionItem{}, errLowScore
	}

	// Handle builtin types separately.
	if obj.Parent() == types.Universe {
		return c.formatBuiltin(ctx, cand)
	}

	var (
		label         = cand.name
		detail        = types.TypeString(obj.Type(), c.qf)
		insert        = label
		kind          = protocol.TextCompletion
		snip          snippet.Builder
		protocolEdits []protocol.TextEdit
	)
	if obj.Type() == nil {
		detail = ""
	}
	if isTypeName(obj) && c.wantTypeParams() {
		x := cand.obj.(*types.TypeName)
		if named, ok := aliases.Unalias(x.Type()).(*types.Named); ok {
			tp := named.TypeParams()
			label += golang.FormatTypeParams(tp)
			insert = label // maintain invariant above (label == insert)
		}
	}

	snip.WriteText(insert)

	switch obj := obj.(type) {
	case *types.TypeName:
		detail, kind = golang.FormatType(obj.Type(), c.qf)
	case *types.Const:
		kind = protocol.ConstantCompletion
	case *types.Var:
		if _, ok := obj.Type().(*types.Struct); ok {
			detail = "struct{...}" // for anonymous unaliased struct types
		} else if obj.IsField() {
			var err error
			detail, err = golang.FormatVarType(ctx, c.snapshot, c.pkg, obj, c.qf, c.mq)
			if err != nil {
				return CompletionItem{}, err
			}
		}
		if obj.IsField() {
			kind = protocol.FieldCompletion
			c.structFieldSnippet(cand, detail, &snip)
		} else {
			kind = protocol.VariableCompletion
		}
		if obj.Type() == nil {
			break
		}
	case *types.Func:
		if obj.Type().(*types.Signature).Recv() == nil {
			kind = protocol.FunctionCompletion
		} else {
			kind = protocol.MethodCompletion
		}
	case *types.PkgName:
		kind = protocol.ModuleCompletion
		detail = fmt.Sprintf("%q", obj.Imported().Path())
	case *types.Label:
		kind = protocol.ConstantCompletion
		detail = "label"
	}

	var prefix string
	for _, mod := range cand.mods {
		switch mod {
		case reference:
			prefix = "&" + prefix
		case dereference:
			prefix = "*" + prefix
		case chanRead:
			prefix = "<-" + prefix
		}
	}

	var (
		suffix   string
		funcType = obj.Type()
	)
Suffixes:
	for _, mod := range cand.mods {
		switch mod {
		case invoke:
			if sig, ok := funcType.Underlying().(*types.Signature); ok {
				s, err := golang.NewSignature(ctx, c.snapshot, c.pkg, sig, nil, c.qf, c.mq)
				if err != nil {
					return CompletionItem{}, err
				}

				tparams := s.TypeParams()
				if len(tparams) > 0 {
					// Eliminate the suffix of type parameters that are
					// likely redundant because they can probably be
					// inferred from the argument types (#51783).
					//
					// We don't bother doing the reverse inference from
					// result types as result-only type parameters are
					// quite unusual.
					free := inferableTypeParams(sig)
					for i := sig.TypeParams().Len() - 1; i >= 0; i-- {
						tparam := sig.TypeParams().At(i)
						if !free[tparam] {
							break
						}
						tparams = tparams[:i] // eliminate
					}
				}

				c.functionCallSnippet("", tparams, s.Params(), &snip)
				if sig.Results().Len() == 1 {
					funcType = sig.Results().At(0).Type()
				}
				detail = "func" + s.Format()
			}

			if !c.opts.snippets {
				// Without snippets the candidate will not include "()". Don't
				// add further suffixes since they will be invalid. For
				// example, with snippets "foo()..." would become "foo..."
				// without snippets if we added the dotDotDot.
				break Suffixes
			}
		case takeSlice:
			suffix += "[:]"
		case takeDotDotDot:
			suffix += "..."
		case index:
			snip.WriteText("[")
			snip.WritePlaceholder(nil)
			snip.WriteText("]")
		}
	}

	// If this candidate needs an additional import statement,
	// add the additional text edits needed.
	if cand.imp != nil {
		addlEdits, err := c.importEdits(cand.imp)

		if err != nil {
			return CompletionItem{}, err
		}

		protocolEdits = append(protocolEdits, addlEdits...)
		if kind != protocol.ModuleCompletion {
			if detail != "" {
				detail += " "
			}
			detail += fmt.Sprintf("(from %q)", cand.imp.importPath)
		}
	}

	if cand.convertTo != nil {
		typeName := types.TypeString(cand.convertTo, c.qf)

		switch t := cand.convertTo.(type) {
		// We need extra parens when casting to these types. For example,
		// we need "(*int)(foo)", not "*int(foo)".
		case *types.Pointer, *types.Signature:
			typeName = "(" + typeName + ")"
		case *types.Basic:
			// If the types are incompatible (as determined by typeMatches), then we
			// must need a conversion here. However, if the target type is untyped,
			// don't suggest converting to e.g. "untyped float" (golang/go#62141).
			if t.Info()&types.IsUntyped != 0 {
				typeName = types.TypeString(types.Default(cand.convertTo), c.qf)
			}
		}

		prefix = typeName + "(" + prefix
		suffix = ")"
	}

	if prefix != "" {
		// If we are in a selector, add an edit to place prefix before selector.
		if sel := enclosingSelector(c.path, c.pos); sel != nil {
			edits, err := c.editText(sel.Pos(), sel.Pos(), prefix)
			if err != nil {
				return CompletionItem{}, err
			}
			protocolEdits = append(protocolEdits, edits...)
		} else {
			// If there is no selector, just stick the prefix at the start.
			insert = prefix + insert
			snip.PrependText(prefix)
		}
	}

	if suffix != "" {
		insert += suffix
		snip.WriteText(suffix)
	}

	detail = strings.TrimPrefix(detail, "untyped ")
	// override computed detail with provided detail, if something is provided.
	if cand.detail != "" {
		detail = cand.detail
	}
	item := CompletionItem{
		Label:               label,
		InsertText:          insert,
		AdditionalTextEdits: protocolEdits,
		Detail:              detail,
		Kind:                kind,
		Score:               cand.score,
		Depth:               len(cand.path),
		snippet:             &snip,
		isSlice:             isSlice(obj),
	}
	// If the user doesn't want documentation for completion items.
	if !c.opts.documentation {
		return item, nil
	}
	pos := safetoken.StartPosition(c.pkg.FileSet(), obj.Pos())

	// We ignore errors here, because some types, like "unsafe" or "error",
	// may not have valid positions that we can use to get documentation.
	if !pos.IsValid() {
		return item, nil
	}

	comment, err := golang.HoverDocForObject(ctx, c.snapshot, c.pkg.FileSet(), obj)
	if err != nil {
		event.Error(ctx, fmt.Sprintf("failed to find Hover for %q", obj.Name()), err)
		return item, nil
	}
	if c.opts.fullDocumentation {
		item.Documentation = comment.Text()
	} else {
		item.Documentation = doc.Synopsis(comment.Text())
	}
	// The desired pattern is `^// Deprecated`, but the prefix has been removed
	// TODO(rfindley): It doesn't look like this does the right thing for
	// multi-line comments.
	if strings.HasPrefix(comment.Text(), "Deprecated") {
		if c.snapshot.Options().CompletionTags {
			item.Tags = []protocol.CompletionItemTag{protocol.ComplDeprecated}
		} else if c.snapshot.Options().CompletionDeprecated {
			item.Deprecated = true
		}
	}

	return item, nil
}

// importEdits produces the text edits necessary to add the given import to the current file.
func (c *completer) importEdits(imp *importInfo) ([]protocol.TextEdit, error) {
	if imp == nil {
		return nil, nil
	}

	pgf, err := c.pkg.File(protocol.URIFromPath(c.filename))
	if err != nil {
		return nil, err
	}

	return golang.ComputeOneImportFixEdits(c.snapshot, pgf, &imports.ImportFix{
		StmtInfo: imports.ImportInfo{
			ImportPath: imp.importPath,
			Name:       imp.name,
		},
		// IdentName is unused on this path and is difficult to get.
		FixType: imports.AddImport,
	})
}

func (c *completer) formatBuiltin(ctx context.Context, cand candidate) (CompletionItem, error) {
	obj := cand.obj
	item := CompletionItem{
		Label:      obj.Name(),
		InsertText: obj.Name(),
		Score:      cand.score,
	}
	switch obj.(type) {
	case *types.Const:
		item.Kind = protocol.ConstantCompletion
	case *types.Builtin:
		item.Kind = protocol.FunctionCompletion
		sig, err := golang.NewBuiltinSignature(ctx, c.snapshot, obj.Name())
		if err != nil {
			return CompletionItem{}, err
		}
		item.Detail = "func" + sig.Format()
		item.snippet = &snippet.Builder{}
		// The signature inferred for a built-in is instantiated, so TypeParams=∅.
		c.functionCallSnippet(obj.Name(), sig.TypeParams(), sig.Params(), item.snippet)
	case *types.TypeName:
		if types.IsInterface(obj.Type()) {
			item.Kind = protocol.InterfaceCompletion
		} else {
			item.Kind = protocol.ClassCompletion
		}
	case *types.Nil:
		item.Kind = protocol.VariableCompletion
	}
	return item, nil
}

// decide if the type params (if any) should be part of the completion
// which only possible for types.Named and types.Signature
// (so far, only in receivers, e.g.; func (s *GENERIC[K, V])..., which is a types.Named)
func (c *completer) wantTypeParams() bool {
	// Need to be lexically in a receiver, and a child of an IndexListExpr
	// (but IndexListExpr only exists with go1.18)
	start := c.path[0].Pos()
	for i, nd := range c.path {
		if fd, ok := nd.(*ast.FuncDecl); ok {
			if i > 0 && fd.Recv != nil && start < fd.Recv.End() {
				return true
			} else {
				return false
			}
		}
	}
	return false
}

// inferableTypeParams returns the set of type parameters
// of sig that are constrained by (inferred from) the argument types.
func inferableTypeParams(sig *types.Signature) map[*types.TypeParam]bool {
	free := make(map[*types.TypeParam]bool)

	// visit adds to free all the free type parameters of t.
	var visit func(t types.Type)
	visit = func(t types.Type) {
		switch t := t.(type) {
		case *types.Array:
			visit(t.Elem())
		case *types.Chan:
			visit(t.Elem())
		case *types.Map:
			visit(t.Key())
			visit(t.Elem())
		case *types.Pointer:
			visit(t.Elem())
		case *types.Slice:
			visit(t.Elem())
		case *types.Interface:
			for i := 0; i < t.NumExplicitMethods(); i++ {
				visit(t.ExplicitMethod(i).Type())
			}
			for i := 0; i < t.NumEmbeddeds(); i++ {
				visit(t.EmbeddedType(i))
			}
		case *types.Union:
			for i := 0; i < t.Len(); i++ {
				visit(t.Term(i).Type())
			}
		case *types.Signature:
			if tp := t.TypeParams(); tp != nil {
				// Generic signatures only appear as the type of generic
				// function declarations, so this isn't really reachable.
				for i := 0; i < tp.Len(); i++ {
					visit(tp.At(i).Constraint())
				}
			}
			visit(t.Params())
			visit(t.Results())
		case *types.Tuple:
			for i := 0; i < t.Len(); i++ {
				visit(t.At(i).Type())
			}
		case *types.Struct:
			for i := 0; i < t.NumFields(); i++ {
				visit(t.Field(i).Type())
			}
		case *types.TypeParam:
			free[t] = true
		case *aliases.Alias:
			visit(aliases.Unalias(t))
		case *types.Named:
			targs := t.TypeArgs()
			for i := 0; i < targs.Len(); i++ {
				visit(targs.At(i))
			}
		case *types.Basic:
			// nop
		default:
			panic(t)
		}
	}

	visit(sig.Params())

	// Perform induction through constraints.
restart:
	for i := 0; i < sig.TypeParams().Len(); i++ {
		tp := sig.TypeParams().At(i)
		if free[tp] {
			n := len(free)
			visit(tp.Constraint())
			if len(free) > n {
				goto restart // iterate until fixed point
			}
		}
	}
	return free
}
