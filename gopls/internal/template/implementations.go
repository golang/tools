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

	"golang.org/x/tools/gopls/internal/cache"
	"golang.org/x/tools/gopls/internal/file"
	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/gopls/internal/protocol/semtok"
)

// line number (1-based) and message
var errRe = regexp.MustCompile(`template.*:(\d+): (.*)`)

// Diagnostics returns parse errors. There is only one per file.
// The errors are not always helpful. For instance { {end}}
// will likely point to the end of the file.
func Diagnostics(snapshot *cache.Snapshot) map[protocol.DocumentURI][]*cache.Diagnostic {
	diags := make(map[protocol.DocumentURI][]*cache.Diagnostic)
	for uri, fh := range snapshot.Templates() {
		diags[uri] = diagnoseOne(fh)
	}
	return diags
}

func diagnoseOne(fh file.Handle) []*cache.Diagnostic {
	// no need for skipTemplate check, as Diagnose is called on the
	// snapshot's template files
	buf, err := fh.Content()
	if err != nil {
		// Is a Diagnostic with no Range useful? event.Error also?
		msg := fmt.Sprintf("failed to read %s (%v)", fh.URI().Path(), err)
		d := cache.Diagnostic{Message: msg, Severity: protocol.SeverityError, URI: fh.URI(),
			Source: cache.TemplateError}
		return []*cache.Diagnostic{&d}
	}
	p := parseBuffer(buf)
	if p.ParseErr == nil {
		return nil
	}
	unknownError := func(msg string) []*cache.Diagnostic {
		s := fmt.Sprintf("malformed template error %q: %s", p.ParseErr.Error(), msg)
		d := cache.Diagnostic{
			Message: s, Severity: protocol.SeverityError, Range: p.Range(p.nls[0], 1),
			URI: fh.URI(), Source: cache.TemplateError}
		return []*cache.Diagnostic{&d}
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
	d := cache.Diagnostic{Message: msg, Severity: protocol.SeverityError,
		Source: cache.TemplateError}
	start := p.nls[lineno-1]
	if lineno < len(p.nls) {
		size := p.nls[lineno] - start
		d.Range = p.Range(start, size)
	} else {
		d.Range = p.Range(start, 1)
	}
	return []*cache.Diagnostic{&d}
}

// Definition finds the definitions of the symbol at loc. It
// does not understand scoping (if any) in templates. This code is
// for definitions, type definitions, and implementations.
// Results only for variables and templates.
func Definition(snapshot *cache.Snapshot, fh file.Handle, loc protocol.Position) ([]protocol.Location, error) {
	x, _, err := symAtPosition(fh, loc)
	if err != nil {
		return nil, err
	}
	sym := x.name
	ans := []protocol.Location{}
	// PJW: this is probably a pattern to abstract
	a := New(snapshot.Templates())
	for k, p := range a.files {
		for _, s := range p.symbols {
			if !s.vardef || s.name != sym {
				continue
			}
			ans = append(ans, protocol.Location{URI: k, Range: p.Range(s.start, s.length)})
		}
	}
	return ans, nil
}

func Hover(ctx context.Context, snapshot *cache.Snapshot, fh file.Handle, position protocol.Position) (*protocol.Hover, error) {
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
		ans.Contents.Value = fmt.Sprintf("%s: field or method", sym.name)
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

func SemanticTokens(ctx context.Context, snapshot *cache.Snapshot, spn protocol.DocumentURI) (*protocol.SemanticTokens, error) {
	fh, err := snapshot.ReadFile(ctx, spn)
	if err != nil {
		return nil, err
	}
	buf, err := fh.Content()
	if err != nil {
		return nil, err
	}
	p := parseBuffer(buf)

	var items []semtok.Token
	add := func(line, start, len uint32) {
		if len == 0 {
			return // vscode doesn't like 0-length Tokens
		}
		// TODO(adonovan): don't ignore the rng restriction, if any.
		items = append(items, semtok.Token{
			Line:  line,
			Start: start,
			Len:   len,
			Type:  semtok.TokMacro,
		})
	}

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
	const noStrings = false
	const noNumbers = false
	ans := &protocol.SemanticTokens{
		Data: semtok.Encode(
			items,
			noStrings,
			noNumbers,
			snapshot.Options().SemanticTypes,
			snapshot.Options().SemanticMods),
		// for small cache, some day. for now, the LSP client ignores this
		// (that is, when the LSP client starts returning these, we can cache)
		ResultID: fmt.Sprintf("%v", time.Now()),
	}
	return ans, nil
}

// still need to do rename, etc
