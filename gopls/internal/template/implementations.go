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
		// TODO: Is a Diagnostic with no Range useful? event.Error also?
		msg := fmt.Sprintf("failed to read %s (%v)", fh.URI().Path(), err)
		return []*cache.Diagnostic{{
			Message:  msg,
			Severity: protocol.SeverityError,
			URI:      fh.URI(),
			Source:   cache.TemplateError,
		}}
	}
	p := parseBuffer(fh.URI(), buf)
	if p.parseErr == nil {
		return nil
	}

	errorf := func(format string, args ...any) []*cache.Diagnostic {
		msg := fmt.Sprintf("malformed template error %q: %s",
			p.parseErr.Error(),
			fmt.Sprintf(format, args...))
		rng, err := p.mapper.OffsetRange(0, 1) // first UTF-16 code
		if err != nil {
			rng = protocol.Range{} // start of file
		}
		return []*cache.Diagnostic{{
			Message:  msg,
			Severity: protocol.SeverityError,
			Range:    rng,
			URI:      fh.URI(),
			Source:   cache.TemplateError,
		}}
	}

	// errors look like `template: :40: unexpected "}" in operand`
	// so the string needs to be parsed
	matches := errRe.FindStringSubmatch(p.parseErr.Error())
	if len(matches) != 3 {
		return errorf("expected 3 matches, got %d (%v)", len(matches), matches)
	}
	lineno, err := strconv.Atoi(matches[1])
	if err != nil {
		return errorf("couldn't convert %q to int, %v", matches[1], err)
	}
	msg := matches[2]

	// Compute the range for the whole (1-based) line.
	rng, err := lineRange(p.mapper, lineno)
	if err != nil {
		return errorf("invalid position: %v", err)
	}

	return []*cache.Diagnostic{{
		Message:  msg,
		Severity: protocol.SeverityError,
		Range:    rng,
		Source:   cache.TemplateError,
	}}
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
	a := parseSet(snapshot.Templates())
	for _, p := range a.files {
		for _, s := range p.symbols {
			if !s.vardef || s.name != sym {
				continue
			}
			loc, err := p.mapper.OffsetLocation(s.offsets())
			if err != nil {
				return nil, err
			}
			ans = append(ans, loc)
		}
	}
	return ans, nil
}

func Hover(ctx context.Context, snapshot *cache.Snapshot, fh file.Handle, position protocol.Position) (*protocol.Hover, error) {
	sym, p, err := symAtPosition(fh, position)
	if err != nil {
		return nil, err
	}

	var value string
	switch sym.kind {
	case protocol.Function:
		value = fmt.Sprintf("function: %s", sym.name)
	case protocol.Variable:
		value = fmt.Sprintf("variable: %s", sym.name)
	case protocol.Constant:
		value = fmt.Sprintf("constant %s", sym.name)
	case protocol.Method: // field or method
		value = fmt.Sprintf("%s: field or method", sym.name)
	case protocol.Package: // template use, template def (PJW: do we want two?)
		value = fmt.Sprintf("template %s\n(add definition)", sym.name)
	case protocol.Namespace:
		value = fmt.Sprintf("template %s defined", sym.name)
	case protocol.Number:
		value = "number"
	case protocol.String:
		value = "string"
	case protocol.Boolean:
		value = "boolean"
	default:
		value = fmt.Sprintf("oops, sym=%#v", sym)
	}

	rng, err := p.mapper.OffsetRange(sym.offsets())
	if err != nil {
		return nil, err
	}

	return &protocol.Hover{
		Range: rng,
		Contents: protocol.MarkupContent{
			Kind:  protocol.Markdown,
			Value: value,
		},
	}, nil
}

func References(ctx context.Context, snapshot *cache.Snapshot, fh file.Handle, params *protocol.ReferenceParams) ([]protocol.Location, error) {
	sym, _, err := symAtPosition(fh, params.Position)
	if err != nil {
		return nil, err
	}
	if sym.name == "" {
		return nil, fmt.Errorf("no symbol at position")
	}
	ans := []protocol.Location{}

	a := parseSet(snapshot.Templates())
	for _, p := range a.files {
		for _, s := range p.symbols {
			if s.name != sym.name {
				continue
			}
			if s.vardef && !params.Context.IncludeDeclaration {
				continue
			}
			loc, err := p.mapper.OffsetLocation(s.offsets())
			if err != nil {
				return nil, err
			}
			ans = append(ans, loc)
		}
	}
	// TODO: do these need to be sorted? (a.files is a map)
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
	p := parseBuffer(fh.URI(), buf)

	var items []semtok.Token
	for _, t := range p.tokens {
		if t.start == t.end {
			continue // vscode doesn't like 0-length tokens
		}
		pos, err := p.mapper.OffsetPosition(t.start)
		if err != nil {
			return nil, err
		}
		// TODO(adonovan): don't ignore the rng restriction, if any.
		items = append(items, semtok.Token{
			Line:  pos.Line,
			Start: pos.Character,
			Len:   uint32(protocol.UTF16Len(p.buf[t.start:t.end])),
			Type:  semtok.TokMacro,
		})
	}
	return &protocol.SemanticTokens{
		Data: semtok.Encode(items, nil, nil),
		// for small cache, some day. for now, the LSP client ignores this
		// (that is, when the LSP client starts returning these, we can cache)
		ResultID: fmt.Sprintf("%v", time.Now()),
	}, nil
}

// TODO: still need to do rename, etc

func symAtPosition(fh file.Handle, posn protocol.Position) (*symbol, *parsed, error) {
	buf, err := fh.Content()
	if err != nil {
		return nil, nil, err
	}
	p := parseBuffer(fh.URI(), buf)
	offset, err := p.mapper.PositionOffset(posn)
	if err != nil {
		return nil, nil, err
	}
	var syms []symbol
	for _, s := range p.symbols {
		if s.start <= offset && offset < s.start+s.len {
			syms = append(syms, s)
		}
	}
	if len(syms) == 0 {
		return nil, p, fmt.Errorf("no symbol found")
	}
	sym := syms[0]
	return &sym, p, nil
}
