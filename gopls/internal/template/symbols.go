// Copyright 2021 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package template

import (
	"bytes"
	"context"
	"fmt"
	"text/template/parse"

	"golang.org/x/tools/gopls/internal/cache"
	"golang.org/x/tools/gopls/internal/file"
	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/internal/event"
)

// in local coordinates, to be translated to protocol.DocumentSymbol
type symbol struct {
	start  int // 0-based byte offset, for sorting
	len    int // of source, in bytes
	name   string
	kind   protocol.SymbolKind
	vardef bool // is this a variable definition?
	// do we care about selection range, or children?
	// no children yet, and selection range is the same as range
}

func (s symbol) offsets() (start, end int) {
	return s.start, s.start + s.len
}

func (s symbol) String() string {
	return fmt.Sprintf("{%d,%d,%s,%s,%v}", s.start, s.len, s.name, s.kind, s.vardef)
}

// for FieldNode or VariableNode (or ChainNode?)
func (p *parsed) fields(flds []string, x parse.Node) []symbol {
	ans := []symbol{}
	// guessing that there are no embedded blanks allowed. The doc is unclear
	lookfor := ""
	switch x.(type) {
	case *parse.FieldNode:
		for _, f := range flds {
			lookfor += "." + f // quadratic, but probably ok
		}
	case *parse.VariableNode:
		lookfor = flds[0]
		for i := 1; i < len(flds); i++ {
			lookfor += "." + flds[i]
		}
	case *parse.ChainNode: // PJW, what are these?
		for _, f := range flds {
			lookfor += "." + f // quadratic, but probably ok
		}
	default:
		// If these happen they will happen even if gopls is restarted
		// and the users does the same thing, so it is better not to panic.
		// context.Background() is used because we don't have access
		// to any other context. [we could, but it would be complicated]
		event.Log(context.Background(), fmt.Sprintf("%T unexpected in fields()", x))
		return nil
	}
	if len(lookfor) == 0 {
		event.Log(context.Background(), fmt.Sprintf("no strings in fields() %#v", x))
		return nil
	}
	startsAt := int(x.Position())
	ix := bytes.Index(p.buf[startsAt:], []byte(lookfor)) // HasPrefix? PJW?
	if ix < 0 || ix > len(lookfor) {                     // lookfor expected to be at start (or so)
		// probably golang.go/#43388, so back up
		startsAt -= len(flds[0]) + 1
		ix = bytes.Index(p.buf[startsAt:], []byte(lookfor)) // ix might be 1? PJW
		if ix < 0 {
			return ans
		}
	}
	at := ix + startsAt
	for _, f := range flds {
		at += 1 // .
		kind := protocol.Method
		if f[0] == '$' {
			kind = protocol.Variable
		}
		sym := symbol{name: f, kind: kind, start: at, len: len(f)}
		if kind == protocol.Variable && len(p.stack) > 1 {
			if pipe, ok := p.stack[len(p.stack)-2].(*parse.PipeNode); ok {
				for _, y := range pipe.Decl {
					if x == y {
						sym.vardef = true
					}
				}
			}
		}
		ans = append(ans, sym)
		at += len(f)
	}
	return ans
}

func (p *parsed) findSymbols() {
	if len(p.stack) == 0 {
		return
	}
	n := p.stack[len(p.stack)-1]
	pop := func() {
		p.stack = p.stack[:len(p.stack)-1]
	}
	if n == nil { // allowing nil simplifies the code
		pop()
		return
	}
	nxt := func(nd parse.Node) {
		p.stack = append(p.stack, nd)
		p.findSymbols()
	}
	switch x := n.(type) {
	case *parse.ActionNode:
		nxt(x.Pipe)
	case *parse.BoolNode:
		// need to compute the length from the value
		msg := fmt.Sprintf("%v", x.True)
		p.symbols = append(p.symbols, symbol{start: int(x.Pos), len: len(msg), kind: protocol.Boolean})
	case *parse.BranchNode:
		nxt(x.Pipe)
		nxt(x.List)
		nxt(x.ElseList)
	case *parse.ChainNode:
		p.symbols = append(p.symbols, p.fields(x.Field, x)...)
		nxt(x.Node)
	case *parse.CommandNode:
		for _, a := range x.Args {
			nxt(a)
		}
	//case *parse.CommentNode: // go 1.16
	//	log.Printf("implement %d", x.Type())
	case *parse.DotNode:
		sym := symbol{name: "dot", kind: protocol.Variable, start: int(x.Pos), len: 1}
		p.symbols = append(p.symbols, sym)
	case *parse.FieldNode:
		p.symbols = append(p.symbols, p.fields(x.Ident, x)...)
	case *parse.IdentifierNode:
		sym := symbol{name: x.Ident, kind: protocol.Function, start: int(x.Pos), len: len(x.Ident)}
		p.symbols = append(p.symbols, sym)
	case *parse.IfNode:
		nxt(&x.BranchNode)
	case *parse.ListNode:
		if x != nil { // wretched typed nils. Node should have an IfNil
			for _, nd := range x.Nodes {
				nxt(nd)
			}
		}
	case *parse.NilNode:
		sym := symbol{name: "nil", kind: protocol.Constant, start: int(x.Pos), len: 3}
		p.symbols = append(p.symbols, sym)
	case *parse.NumberNode:
		// no name; ascii
		p.symbols = append(p.symbols, symbol{start: int(x.Pos), len: len(x.Text), kind: protocol.Number})
	case *parse.PipeNode:
		if x == nil { // {{template "foo"}}
			return
		}
		for _, d := range x.Decl {
			nxt(d)
		}
		for _, c := range x.Cmds {
			nxt(c)
		}
	case *parse.RangeNode:
		nxt(&x.BranchNode)
	case *parse.StringNode:
		// no name
		p.symbols = append(p.symbols, symbol{start: int(x.Pos), len: len(x.Quoted), kind: protocol.String})
	case *parse.TemplateNode:
		// invoking a template, e.g. {{define "foo"}}
		// x.Pos is the index of "foo".
		// The logic below assumes that the literal is trivial.
		p.symbols = append(p.symbols, symbol{name: x.Name, kind: protocol.Package, start: int(x.Pos) + len(`"`), len: len(x.Name)})
		nxt(x.Pipe)
	case *parse.TextNode:
		if len(x.Text) == 1 && x.Text[0] == '\n' {
			break
		}
		// nothing to report, but build one for hover
		p.symbols = append(p.symbols, symbol{start: int(x.Pos), len: len(x.Text), kind: protocol.Constant})
	case *parse.VariableNode:
		p.symbols = append(p.symbols, p.fields(x.Ident, x)...)
	case *parse.WithNode:
		nxt(&x.BranchNode)
	}
	pop()
}

// DocumentSymbols returns a hierarchy of the symbols defined in a template file.
// (The hierarchy is flat. SymbolInformation might be better.)
func DocumentSymbols(snapshot *cache.Snapshot, fh file.Handle) ([]protocol.DocumentSymbol, error) {
	buf, err := fh.Content()
	if err != nil {
		return nil, err
	}
	p := parseBuffer(fh.URI(), buf)
	if p.parseErr != nil {
		return nil, p.parseErr
	}
	var ans []protocol.DocumentSymbol
	for _, sym := range p.symbols {
		if sym.kind == protocol.Constant {
			continue
		}
		detail := kindStr(sym.kind)
		if detail == "Namespace" {
			detail = "Template"
		}
		if sym.vardef {
			detail += "(def)"
		} else {
			detail += "(use)"
		}
		rng, err := p.mapper.OffsetRange(sym.offsets())
		if err != nil {
			return nil, err
		}
		ans = append(ans, protocol.DocumentSymbol{
			Name:           sym.name,
			Detail:         detail,
			Kind:           sym.kind,
			Range:          rng,
			SelectionRange: rng, // or should this be the entire {{...}}?
		})
	}
	return ans, nil
}

func kindStr(k protocol.SymbolKind) string {
	n := int(k)
	if n < 1 || n >= len(kindNames) {
		return fmt.Sprintf("?SymbolKind %d?", n)
	}
	return kindNames[n]
}

var kindNames = []string{
	"",
	"File",
	"Module",
	"Namespace",
	"Package",
	"Class",
	"Method",
	"Property",
	"Field",
	"Constructor",
	"Enum",
	"Interface",
	"Function",
	"Variable",
	"Constant",
	"String",
	"Number",
	"Boolean",
	"Array",
	"Object",
	"Key",
	"Null",
	"EnumMember",
	"Struct",
	"Event",
	"Operator",
	"TypeParameter",
}
