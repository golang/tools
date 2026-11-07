// Copyright 2021 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package template contains code for dealing with templates
package template

// template files are small enough that the code reprocesses them each time
// this may be a bad choice for projects with lots of template files.

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"regexp"
	"sort"
	"text/template"
	"text/template/parse"

	"golang.org/x/tools/gopls/internal/file"
	"golang.org/x/tools/gopls/internal/protocol"
)

var (
	lbraces = []byte("{{")
	rbraces = []byte("}}")
)

type parsed struct {
	buf    []byte // contents
	mapper *protocol.Mapper
	elided []int // offsets where lbraces was replaced by blanks

	// tokens are matched lbraces-rbraces pairs, computed before trying to parse
	tokens []token

	// result of parsing
	named    []*template.Template // the template and embedded templates
	parseErr error
	symbols  []symbol
	stack    []parse.Node // used while computing symbols
}

// A token is a single {{...}}.
type token struct {
	start, end int // 0-based byte offset from start of template
}

// set contains the Parse of all the template files
type set struct {
	files map[protocol.DocumentURI]*parsed
}

// parseSet returns the set of the snapshot's tmpl files
// (maybe cache these, but then avoiding import cycles needs code rearrangements)
//
// TODO(adonovan): why doesn't parseSet return an error?
func parseSet(tmpls map[protocol.DocumentURI]file.Handle) *set {
	all := make(map[protocol.DocumentURI]*parsed)
	for uri, fh := range tmpls {
		buf, err := fh.Content()
		if err != nil {
			// TODO(pjw): decide what to do with these errors
			log.Printf("failed to read %s (%v)", fh.URI().Path(), err)
			continue
		}
		all[uri] = parseBuffer(uri, buf)
	}
	return &set{files: all}
}

func parseBuffer(uri protocol.DocumentURI, buf []byte) *parsed {
	ans := &parsed{
		buf:    buf,
		mapper: protocol.NewMapper(uri, buf),
	}
	if len(buf) == 0 {
		return ans
	}
	ans.setTokens() // ans.buf may be a new []byte
	t, err := template.New("").Parse(string(ans.buf))
	if err != nil {
		funcs := make(template.FuncMap)
		for t == nil && ans.parseErr == nil {
			// in 1.17 it may be possible to avoid getting this error
			//  template: :2: function "foo" not defined
			matches := parseErrR.FindStringSubmatch(err.Error())
			if len(matches) == 2 {
				// suppress the error by giving it a function with the right name
				funcs[matches[1]] = func() any { return nil }
				t, err = template.New("").Funcs(funcs).Parse(string(ans.buf))
				continue
			}
			ans.parseErr = err // unfixed error
			return ans
		}
	}
	ans.named = t.Templates()
	// set the symbols
	for _, t := range ans.named {
		ans.stack = append(ans.stack, t.Root)
		ans.findSymbols()
		if t.Name() != "" {
			// defining a template. The pos is just after {{define...}} (or {{block...}}?)
			at, sz := ans.findLiteralBefore(int(t.Root.Pos))
			s := symbol{start: at, len: sz, name: t.Name(), kind: protocol.Namespace, vardef: true}
			ans.symbols = append(ans.symbols, s)
		}
	}

	sort.Slice(ans.symbols, func(i, j int) bool {
		left, right := ans.symbols[i], ans.symbols[j]
		if left.start != right.start {
			return left.start < right.start
		}
		if left.vardef != right.vardef {
			return left.vardef
		}
		return left.kind < right.kind
	})
	return ans
}

// findLiteralBefore locates the first preceding string literal
// returning its offset and length in buf or (-1, 0) if there is none.
// Assume double-quoted string rather than backquoted string for now.
func (p *parsed) findLiteralBefore(pos int) (int, int) {
	left, right := -1, -1
	for i := pos - 1; i >= 0; i-- {
		if p.buf[i] != '"' {
			continue
		}
		if right == -1 {
			right = i
			continue
		}
		left = i
		break
	}
	if left == -1 {
		return -1, 0
	}
	return left + 1, right - left - 1
}

var (
	parseErrR = regexp.MustCompile(`template:.*function "([^"]+)" not defined`)
)

func (p *parsed) setTokens() {
	const (
		// InRaw and InString only occur inside an action (SeenLeft)
		Start = iota
		InRaw
		InString
		SeenLeft
	)
	state := Start
	var left, oldState int
	for n := 0; n < len(p.buf); n++ {
		c := p.buf[n]
		switch state {
		case InRaw:
			if c == '`' {
				state = oldState
			}
		case InString:
			if c == '"' && !isEscaped(p.buf[:n]) {
				state = oldState
			}
		case SeenLeft:
			if c == '`' {
				oldState = state // it's SeenLeft, but a little clearer this way
				state = InRaw
				continue
			}
			if c == '"' {
				oldState = state
				state = InString
				continue
			}
			if bytes.HasPrefix(p.buf[n:], rbraces) {
				right := n + len(rbraces)
				tok := token{start: left, end: right}
				p.tokens = append(p.tokens, tok)
				state = Start
			}
			// If we see (unquoted) lbraces then the original left is probably the user
			// typing. Suppress the original left
			if bytes.HasPrefix(p.buf[n:], lbraces) {
				p.elideAt(left)
				left = n
				n += len(lbraces) - 1 // skip the rest
			}
		case Start:
			if bytes.HasPrefix(p.buf[n:], lbraces) {
				left = n
				state = SeenLeft
				n += len(lbraces) - 1 // skip the rest (avoids {{{ bug)
			}
		}
	}
	// this error occurs after typing {{ at the end of the file
	if state != Start {
		// Unclosed lbraces. remove the lbraces at left
		p.elideAt(left)
	}
}

func (p *parsed) elideAt(left int) {
	if p.elided == nil {
		// p.buf is the same buffer that v.Read() returns, so copy it.
		// (otherwise the next time it's parsed, elided information is lost)
		p.buf = bytes.Clone(p.buf)
	}
	for i := range lbraces {
		p.buf[left+i] = ' '
	}
	p.elided = append(p.elided, left)
}

// isEscaped reports whether the byte after buf is escaped
func isEscaped(buf []byte) bool {
	backSlashes := 0
	for j := len(buf) - 1; j >= 0 && buf[j] == '\\'; j-- {
		backSlashes++
	}
	return backSlashes%2 == 1
}

// lineRange returns the range for the entire specified (1-based) line.
func lineRange(m *protocol.Mapper, line int) (protocol.Range, error) {
	posn := protocol.Position{Line: uint32(line - 1)}

	// start of line
	start, err := m.PositionOffset(posn)
	if err != nil {
		return protocol.Range{}, err
	}

	// end of line (or file)
	posn.Line++
	end := len(m.Content) // EOF
	if offset, err := m.PositionOffset(posn); err != nil {
		end = offset - len("\n")
	}

	return m.OffsetRange(start, end)
}

// -- debugging --

func (p *parsed) writeNode(w io.Writer, n parse.Node) {
	wr := wrNode{p: p, w: w}
	wr.writeNode(n, "")
}

type wrNode struct {
	p *parsed
	w io.Writer
}

func (wr wrNode) writeNode(n parse.Node, indent string) {
	if n == nil {
		return
	}
	at := func(pos parse.Pos) string {
		offset := int(pos)
		posn, err := wr.p.mapper.OffsetPosition(offset)
		if err != nil {
			return fmt.Sprintf("<bad pos %d: %v>", pos, err)
		}
		return fmt.Sprintf("(%d)%v:%v", pos, posn.Line, posn.Character)
	}
	switch x := n.(type) {
	case *parse.ActionNode:
		fmt.Fprintf(wr.w, "%sActionNode at %s\n", indent, at(x.Pos))
		wr.writeNode(x.Pipe, indent+". ")
	case *parse.BoolNode:
		fmt.Fprintf(wr.w, "%sBoolNode at %s, %v\n", indent, at(x.Pos), x.True)
	case *parse.BranchNode:
		fmt.Fprintf(wr.w, "%sBranchNode at %s\n", indent, at(x.Pos))
		wr.writeNode(x.Pipe, indent+"Pipe. ")
		wr.writeNode(x.List, indent+"List. ")
		wr.writeNode(x.ElseList, indent+"Else. ")
	case *parse.ChainNode:
		fmt.Fprintf(wr.w, "%sChainNode at %s, %v\n", indent, at(x.Pos), x.Field)
	case *parse.CommandNode:
		fmt.Fprintf(wr.w, "%sCommandNode at %s, %d children\n", indent, at(x.Pos), len(x.Args))
		for _, a := range x.Args {
			wr.writeNode(a, indent+". ")
		}
	//case *parse.CommentNode: // 1.16
	case *parse.DotNode:
		fmt.Fprintf(wr.w, "%sDotNode at %s\n", indent, at(x.Pos))
	case *parse.FieldNode:
		fmt.Fprintf(wr.w, "%sFieldNode at %s, %v\n", indent, at(x.Pos), x.Ident)
	case *parse.IdentifierNode:
		fmt.Fprintf(wr.w, "%sIdentifierNode at %s, %v\n", indent, at(x.Pos), x.Ident)
	case *parse.IfNode:
		fmt.Fprintf(wr.w, "%sIfNode at %s\n", indent, at(x.Pos))
		wr.writeNode(&x.BranchNode, indent+". ")
	case *parse.ListNode:
		if x == nil {
			return // nil BranchNode.ElseList
		}
		fmt.Fprintf(wr.w, "%sListNode at %s, %d children\n", indent, at(x.Pos), len(x.Nodes))
		for _, n := range x.Nodes {
			wr.writeNode(n, indent+". ")
		}
	case *parse.NilNode:
		fmt.Fprintf(wr.w, "%sNilNode at %s\n", indent, at(x.Pos))
	case *parse.NumberNode:
		fmt.Fprintf(wr.w, "%sNumberNode at %s, %s\n", indent, at(x.Pos), x.Text)
	case *parse.PipeNode:
		if x == nil {
			return // {{template "xxx"}}
		}
		fmt.Fprintf(wr.w, "%sPipeNode at %s, %d vars, %d cmds, IsAssign:%v\n",
			indent, at(x.Pos), len(x.Decl), len(x.Cmds), x.IsAssign)
		for _, d := range x.Decl {
			wr.writeNode(d, indent+"Decl. ")
		}
		for _, c := range x.Cmds {
			wr.writeNode(c, indent+"Cmd. ")
		}
	case *parse.RangeNode:
		fmt.Fprintf(wr.w, "%sRangeNode at %s\n", indent, at(x.Pos))
		wr.writeNode(&x.BranchNode, indent+". ")
	case *parse.StringNode:
		fmt.Fprintf(wr.w, "%sStringNode at %s, %s\n", indent, at(x.Pos), x.Quoted)
	case *parse.TemplateNode:
		fmt.Fprintf(wr.w, "%sTemplateNode at %s, %s\n", indent, at(x.Pos), x.Name)
		wr.writeNode(x.Pipe, indent+". ")
	case *parse.TextNode:
		fmt.Fprintf(wr.w, "%sTextNode at %s, len %d\n", indent, at(x.Pos), len(x.Text))
	case *parse.VariableNode:
		fmt.Fprintf(wr.w, "%sVariableNode at %s, %v\n", indent, at(x.Pos), x.Ident)
	case *parse.WithNode:
		fmt.Fprintf(wr.w, "%sWithNode at %s\n", indent, at(x.Pos))
		wr.writeNode(&x.BranchNode, indent+". ")
	}
}
