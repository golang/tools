// Copyright 2021 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package template

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"time"

	"golang.org/x/tools/gopls/internal/file"
	"golang.org/x/tools/gopls/internal/lsp/cache"
	"golang.org/x/tools/gopls/internal/lsp/protocol"
	"golang.org/x/tools/gopls/internal/lsp/source"
)

// line number (1-based) and message
var errRe = regexp.MustCompile(`template.*:(\d+): (.*)`)

// Diagnose returns parse errors. There is only one.
// The errors are not always helpful. For instance { {end}}
// will likely point to the end of the file.
func Diagnose(f file.Handle) []*source.Diagnostic {
	// no need for skipTemplate check, as Diagnose is called on the
	// snapshot's template files
	buf, err := f.Content()
	if err != nil {
		// Is a Diagnostic with no Range useful? event.Error also?
		msg := fmt.Sprintf("failed to read %s (%v)", f.URI().Path(), err)
		d := source.Diagnostic{Message: msg, Severity: protocol.SeverityError, URI: f.URI(),
			Source: source.TemplateError}
		return []*source.Diagnostic{&d}
	}
	p := parseBuffer(buf)
	if p.ParseErr == nil {
		return nil
	}
	unknownError := func(msg string) []*source.Diagnostic {
		s := fmt.Sprintf("malformed template error %q: %s", p.ParseErr.Error(), msg)
		d := source.Diagnostic{
			Message: s, Severity: protocol.SeverityError, Range: p.Range(p.nls[0], 1),
			URI: f.URI(), Source: source.TemplateError}
		return []*source.Diagnostic{&d}
	}
	// errors look like `template: :40: unexpected "}" in operand`
	// so the string needs to be parsed
	matches := errRe.FindStringSubmatch(p.ParseErr.Error())
	if len(matches) != 3 {
		msg := fmt.Sprintf("expected 3 matches, got %d (%v)", len(matches), matches)
		return unknownError(msg)
	}
	lineno, err := strconv.Atoi(matches[1])
	if err != nil {
		msg := fmt.Sprintf("couldn't convert %q to int, %v", matches[1], err)
		return unknownError(msg)
	}
	msg := matches[2]
	d := source.Diagnostic{Message: msg, Severity: protocol.SeverityError,
		Source: source.TemplateError}
	start := p.nls[lineno-1]
	if lineno < len(p.nls) {
		size := p.nls[lineno] - start
		d.Range = p.Range(start, size)
	} else {
		d.Range = p.Range(start, 1)
	}
	return []*source.Diagnostic{&d}
}

// Definition finds the definitions of the symbol at loc. It
// does not understand scoping (if any) in templates. This code is
// for definitions, type definitions, and implementations.
// Results only for variables and templates.
func Definition(snapshot *cache.Snapshot, fh file.Handle, loc protocol.Position) ([]protocol.Location, error) {
	sym, p, err := symAtPosition(fh, loc)
	if err != nil {
		return nil, err
	}
	ans := []protocol.Location{}
	switch sym.kind {
	case protocol.Method:
		workspaceSymbol, err := workspaceSymbolForMethod(context.Background(), sym, p, snapshot)
		if err != nil {
			return nil, err
		}
		if workspaceSymbol == nil {
			return ans, nil
		}
		ans = append(ans, workspaceSymbol.Location)
	default:
		// PJW: this is probably a pattern to abstract
		a := New(snapshot.Templates())
		for k, p := range a.files {
			for _, s := range p.symbols {
				if !s.vardef || s.name != sym.name {
					continue
				}
				ans = append(ans, protocol.Location{URI: k, Range: p.Range(s.start, s.length)})
			}
		}
	}
	return ans, nil
}

func workspaceSymbolForMethod(ctx context.Context, sym *symbol, p *Parsed, snapshot source.Snapshot) (*protocol.SymbolInformation, error) {
	if p.goTypePackage == "" || p.goTypeName == "" {
		return nil, nil
	}
	// TODO(mortenson): Support deeply nested fields
	query := p.goTypePackage + "." + p.goTypeName + "." + sym.name
	symbols, err := source.WorkspaceSymbols(ctx, snapshot.Options().SymbolMatcher, snapshot.Options().SymbolStyle, []source.Snapshot{snapshot}, query)
	if err != nil || len(symbols) == 0 {
		return nil, err
	}
	return &symbols[0], nil
}

func Hover(ctx context.Context, snapshot source.Snapshot, fh file.Handle, position protocol.Position) (*protocol.Hover, error) {
	sym, p, err := symAtPosition(fh, position)
	if sym == nil || err != nil {
		return nil, err
	}
	ans := protocol.Hover{Range: p.Range(sym.start, sym.length), Contents: protocol.MarkupContent{Kind: protocol.Markdown}}
	switch sym.kind {
	case protocol.Function:
		ans.Contents.Value = fmt.Sprintf("function: %s", sym.name)
	case protocol.Variable:
		ans.Contents.Value = fmt.Sprintf("variable: %s", sym.name)
	case protocol.Constant:
		ans.Contents.Value = fmt.Sprintf("constant %s", sym.name)
	case protocol.Method: // field or method
		workspaceSymbol, err := workspaceSymbolForMethod(ctx, sym, p, snapshot)
		if err != nil {
			return nil, err
		}
		if workspaceSymbol == nil {
			ans.Contents.Value = fmt.Sprintf("%s: field or method", sym.name)
		} else {
			fh, err := snapshot.ReadFile(ctx, workspaceSymbol.Location.URI)
			if err != nil {
				return nil, err
			}
			formattedHover, err := source.Hover(ctx, snapshot, fh, workspaceSymbol.Location.Range.Start)
			if err != nil {
				return nil, err
			}
			ans.Contents.Value = formattedHover.Contents.Value
		}
	case protocol.Package: // template use, template def (PJW: do we want two?)
		ans.Contents.Value = fmt.Sprintf("template %s\n(add definition)", sym.name)
	case protocol.Namespace:
		ans.Contents.Value = fmt.Sprintf("template %s defined", sym.name)
	case protocol.Number:
		ans.Contents.Value = "number"
	case protocol.String:
		ans.Contents.Value = "string"
	case protocol.Boolean:
		ans.Contents.Value = "boolean"
	default:
		ans.Contents.Value = fmt.Sprintf("oops, sym=%#v", sym)
	}
	return &ans, nil
}

func References(ctx context.Context, snapshot *cache.Snapshot, fh file.Handle, params *protocol.ReferenceParams) ([]protocol.Location, error) {
	sym, _, err := symAtPosition(fh, params.Position)
	if sym == nil || err != nil || sym.name == "" {
		return nil, err
	}
	ans := []protocol.Location{}

	a := New(snapshot.Templates())
	for k, p := range a.files {
		for _, s := range p.symbols {
			if s.name != sym.name {
				continue
			}
			if s.vardef && !params.Context.IncludeDeclaration {
				continue
			}
			ans = append(ans, protocol.Location{URI: k, Range: p.Range(s.start, s.length)})
		}
	}
	// do these need to be sorted? (a.files is a map)
	return ans, nil
}

func SemanticTokens(ctx context.Context, snapshot source.Snapshot, spn protocol.DocumentURI, add func(line, start, len uint32), d func() []uint32) (*protocol.SemanticTokens, error) {
	fh, err := snapshot.ReadFile(ctx, spn)
	if err != nil {
		return nil, err
	}
	buf, err := fh.Content()
	if err != nil {
		return nil, err
	}
	p := parseBuffer(buf)

	for _, t := range p.Tokens() {
		if t.Multiline {
			la, ca := p.LineCol(t.Start)
			lb, cb := p.LineCol(t.End)
			add(la, ca, p.RuneCount(la, ca, 0))
			for l := la + 1; l < lb; l++ {
				add(l, 0, p.RuneCount(l, 0, 0))
			}
			add(lb, 0, p.RuneCount(lb, 0, cb))
			continue
		}
		sz, err := p.TokenSize(t)
		if err != nil {
			return nil, err
		}
		line, col := p.LineCol(t.Start)
		add(line, col, uint32(sz))
	}
	data := d()
	ans := &protocol.SemanticTokens{
		Data: data,
		// for small cache, some day. for now, the LSP client ignores this
		// (that is, when the LSP client starts returning these, we can cache)
		ResultID: fmt.Sprintf("%v", time.Now()),
	}
	return ans, nil
}

// still need to do rename, etc
