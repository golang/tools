// Copyright 2017 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package render

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/doc"
	"go/printer"
	"go/scanner"
	"go/token"
	"html/template"
	"io"
	"regexp"
	"strconv"
	"strings"
)

/*
This logic is responsible for converting documentation comments and AST nodes
into formatted HTML. This relies on identifierResolver.toHTML to do the work
of converting words into links.
*/

// TODO: Support hiding deprecated declarations (https:/golang.org/issue/17056).

const (
	// Regexp for URLs.
	// Match any ".,:;?!" within path, but not at end (see #18139, #16565).
	// This excludes some rare yet valid URLs ending in common punctuation
	// in order to allow sentences ending in URLs.
	urlRx = protoPart + `://` + hostPart + pathPart

	// Protocol (e.g. "http").
	protoPart = `(https?|s?ftps?|file|gopher|mailto|nntp)`
	// Host (e.g. "www.example.com" or "[::1]:8080").
	hostPart = `([a-zA-Z0-9_@\-.\[\]:]+)`
	// Optional path, query, fragment (e.g. "/path/index.html?q=foo#bar").
	pathPart = `([.,:;?!]*[a-zA-Z0-9$'()*+&#=@~_/\-\[\]%])*`

	// Regexp for Go identifiers.
	identRx     = `[\pL_][\pL_0-9]*`
	qualIdentRx = identRx + `(\.` + identRx + `)*`
)

var (
	matchRx     = regexp.MustCompile(urlRx + `|` + qualIdentRx)
	badAnchorRx = regexp.MustCompile(`[^a-zA-Z0-9]`)
)

func (r *Renderer) declHTML(doc string, decl ast.Decl) (out struct{ Doc, Decl template.HTML }) {
	dids := newDeclIDs(decl)
	idr := &identifierResolver{r.pids, dids, r.packageURL}
	if doc != "" {
		var b bytes.Buffer
		for _, blk := range docToBlocks(doc) {
			switch blk := blk.(type) {
			case *paragraph:
				b.WriteString("<p>\n")
				for _, line := range blk.lines {
					r.formatLineHTML(&b, line, idr)
					b.WriteString("\n")
				}
				b.WriteString("</p>\n")
			case *preformat:
				b.WriteString("<pre>\n")
				for _, line := range blk.lines {
					r.formatLineHTML(&b, line, nil)
					b.WriteString("\n")
				}
				b.WriteString("</pre>\n")
			case *heading:
				id := badAnchorRx.ReplaceAllString(blk.title, "_")
				b.WriteString(`<h3 id="hdr-` + id + `">`)
				b.WriteString(template.HTMLEscapeString(blk.title))
				b.WriteString("</h3>\n")
			}
		}
		out.Doc = template.HTML(b.String())
	}
	if decl != nil {
		var b bytes.Buffer
		b.WriteString("<pre>\n")
		r.formatDeclHTML(&b, decl, idr)
		b.WriteString("</pre>\n")
		out.Decl = template.HTML(b.String())
	}
	return out
}

func (r *Renderer) codeHTML(code interface{}) template.HTML {
	// TODO: Should we perform hotlinking for comments and code?
	if code == nil {
		return ""
	}

	var b bytes.Buffer
	p := printer.Config{Mode: printer.UseSpaces, Tabwidth: 4}
	p.Fprint(&b, r.fset, code)
	src := b.String()

	// If code is an *ast.BlockStmt, then trim the braces.
	var indent string
	if len(src) >= 4 && strings.HasPrefix(src, "{\n") && strings.HasSuffix(src, "\n}") {
		src = strings.Trim(src[2:len(src)-2], "\n")
		indent = src[:indentLength(src)]
		if len(indent) > 0 {
			src = strings.TrimPrefix(src, indent) // handle remaining indents later
		}
	}

	// Scan through the source code, adding comment spans for comments,
	// and stripping the trailing example output.
	var bb bytes.Buffer
	var lastOffset int   // last src offset copied to output buffer
	var outputOffset int // index in output buffer of output comment
	var s scanner.Scanner
	fset := token.NewFileSet()
	file := fset.AddFile("", fset.Base(), len(src))
	s.Init(file, []byte(src), nil, scanner.ScanComments)
	bb.WriteString("<pre>\n")
	indent = "\n" + indent // prepend newline for easier search-and-replace.
scan:
	for {
		p, tok, lit := s.Scan()
		offset := file.Offset(p) // current offset into source file
		prev := src[lastOffset:offset]
		prev = strings.Replace(prev, indent, "\n", -1)
		bb.WriteString(template.HTMLEscapeString(prev))
		lastOffset = offset
		switch tok {
		case token.EOF:
			break scan
		case token.COMMENT:
			if exampleOutputRx.MatchString(lit) && outputOffset == 0 {
				outputOffset = bb.Len()
			}
			bb.WriteString(`<span class="comment">`)
			lit = strings.Replace(lit, indent, "\n", -1)
			bb.WriteString(template.HTMLEscapeString(lit))
			bb.WriteString(`</span>`)
			lastOffset += len(lit)
		case token.STRING:
			// Avoid replacing indents in multi-line string literals.
			outputOffset = 0
			bb.WriteString(template.HTMLEscapeString(lit))
			lastOffset += len(lit)
		default:
			outputOffset = 0
		}
	}

	if outputOffset > 0 {
		bb.Truncate(outputOffset)
	}
	for bb.Len() > 0 && bb.Bytes()[bb.Len()-1] == '\n' {
		bb.Truncate(bb.Len() - 1) // trim trailing newlines
	}
	bb.WriteByte('\n')
	bb.WriteString("</pre>\n")
	return template.HTML(bb.String())
}

// formatLineHTML formats the line as HTML-annotated text.
// URLs and Go identifiers are linked to corresponding declarations.
func (*Renderer) formatLineHTML(w io.Writer, line string, idr *identifierResolver) {
	var lastChar, nextChar byte
	var numQuotes int
	for len(line) > 0 {
		m0, m1 := len(line), len(line)
		if m := matchRx.FindStringIndex(line); m != nil {
			m0, m1 = m[0], m[1]
		}
		if m0 > 0 {
			nonWord := line[:m0]
			io.WriteString(w, template.HTMLEscapeString(nonWord))
			lastChar = nonWord[len(nonWord)-1]
			numQuotes += countQuotes(nonWord)
		}
		if m1 > m0 {
			word := line[m0:m1]
			nextChar = 0
			if m1 < len(line) {
				nextChar = line[m1]
			}

			// Reduce false-positives by having a whitelist of
			// valid characters preceding and succeeding an identifier.
			// Also, forbid ID linking within unbalanced quotes on same line.
			validPrefix := strings.IndexByte("\x00 \t()[]*\n", lastChar) >= 0
			validSuffix := strings.IndexByte("\x00 \t()[]:;,.'\n", nextChar) >= 0
			forbidLinking := !validPrefix || !validSuffix || numQuotes%2 != 0

			// TODO: Should we provide hotlinks for related packages?

			switch {
			case strings.Contains(word, "://"):
				// Forbid closing brackets without prior opening brackets.
				// See https://golang.org/issue/22285.
				if i := strings.IndexByte(word, ')'); i >= 0 && i < strings.IndexByte(word, '(') {
					m1 = m0 + i
					word = line[m0:m1]
				}
				if i := strings.IndexByte(word, ']'); i >= 0 && i < strings.IndexByte(word, '[') {
					m1 = m0 + i
					word = line[m0:m1]
				}

				// Require balanced pairs of parentheses.
				// See https://golang.org/issue/5043.
				for i := 0; strings.Count(word, "(") != strings.Count(word, ")") && i < 10; i++ {
					m1 = strings.LastIndexAny(line[:m1], "()")
					word = line[m0:m1]
				}
				for i := 0; strings.Count(word, "[") != strings.Count(word, "]") && i < 10; i++ {
					m1 = strings.LastIndexAny(line[:m1], "[]")
					word = line[m0:m1]
				}

				word := template.HTMLEscapeString(word)
				fmt.Fprintf(w, `<a href="%s">%s</a>`, word, word)
			case !forbidLinking && idr != nil: // && numQuotes%2 == 0:
				io.WriteString(w, idr.toHTML(word))
			default:
				io.WriteString(w, template.HTMLEscapeString(word))
			}
			numQuotes += countQuotes(word)
		}
		line = line[m1:]
	}
}

func countQuotes(s string) int {
	n := -1 // loop always iterates at least once
	for i := len(s); i >= 0; i = strings.LastIndexAny(s[:i], `"“”`) {
		n++
	}
	return n
}

// formatDeclHTML formats the decl as HTML-annotated source code for the
// provided decl. Type identifiers are linked to corresponding declarations.
func (r *Renderer) formatDeclHTML(w io.Writer, decl ast.Decl, idr *identifierResolver) {
	// TODO: Disable anchors for type and func declarations?

	// Generate all anchor points and links for the given decl.
	anchorPointsMap := generateAnchorPoints(decl)
	anchorLinksMap := generateAnchorLinks(idr, decl)

	// Convert the maps (keyed by *ast.Ident) to slices of IDs or URLs.
	//
	// This relies on the ast.Inspect and scanner.Scanner both
	// visiting *ast.Ident and token.IDENT nodes in the same order.
	var anchorPoints, anchorLinks []string
	ast.Inspect(decl, func(node ast.Node) bool {
		if id, ok := node.(*ast.Ident); ok {
			anchorPoints = append(anchorPoints, anchorPointsMap[id])
			anchorLinks = append(anchorLinks, anchorLinksMap[id])
		}
		return true
	})

	// Format decl as Go source code file.
	var b bytes.Buffer
	p := printer.Config{Mode: printer.UseSpaces, Tabwidth: 4}
	p.Fprint(&b, r.fset, decl)
	src := b.Bytes()
	fset := token.NewFileSet()
	file := fset.AddFile("", fset.Base(), b.Len())

	// anchorLines is a list of anchor IDs that should be placed for each line.
	// lineTypes is a list of the type (e.g., comment or code) of each line.
	type lineType byte
	const codeType, commentType lineType = 1 << 0, 1 << 1 // may OR together
	numLines := bytes.Count(src, []byte("\n")) + 1
	anchorLines := make([][]string, numLines)
	lineTypes := make([]lineType, numLines)

	// Scan through the source code, appropriately annotating it with HTML spans
	// for comments, and HTML links and anchors for relevant identifiers.
	var bb bytes.Buffer // temporary output buffer
	var idIdx int       // current index in anchorPoints and anchorLinks
	var lastOffset int  // last src offset copied to output buffer
	var s scanner.Scanner
	s.Init(file, src, nil, scanner.ScanComments)
scan:
	for {
		p, tok, lit := s.Scan()
		line := file.Line(p) - 1 // current 0-indexed line number
		offset := file.Offset(p) // current offset into source file
		tokType := codeType      // current token type (assume source code)

		template.HTMLEscape(&bb, src[lastOffset:offset])
		lastOffset = offset
		switch tok {
		case token.EOF:
			break scan
		case token.COMMENT:
			tokType = commentType
			bb.WriteString(`<span class="comment">`)
			r.formatLineHTML(&bb, lit, idr)
			bb.WriteString(`</span>`)
			lastOffset += len(lit)
		case token.IDENT:
			if idIdx < len(anchorPoints) && anchorPoints[idIdx] != "" {
				anchorLines[line] = append(anchorLines[line], anchorPoints[idIdx])
			}
			if idIdx < len(anchorLinks) && anchorLinks[idIdx] != "" {
				u := template.HTMLEscapeString(anchorLinks[idIdx])
				s := template.HTMLEscapeString(lit)
				fmt.Fprintf(&bb, `<a href="%s">%s</a>`, u, s)
				lastOffset += len(lit)
			}
			idIdx++
		}
		for i := strings.Count(strings.TrimSuffix(lit, "\n"), "\n"); i >= 0; i-- {
			lineTypes[line+i] |= tokType
		}
	}

	// Move anchor points up to the start of a comment
	// if the next line has no anchors.
	for i := range anchorLines {
		if i+1 == len(anchorLines) || len(anchorLines[i+1]) == 0 {
			j := i
			for j > 0 && lineTypes[j-1] == commentType {
				j--
			}
			anchorLines[i], anchorLines[j] = anchorLines[j], anchorLines[i]
		}
	}

	// For each line, emit anchor IDs for each relevant line.
	for _, ids := range anchorLines {
		for _, id := range ids {
			id = template.HTMLEscapeString(id)
			fmt.Fprintf(w, `<span id="%s"></span>`, id)
		}
		b, _ := bb.ReadBytes('\n')
		w.Write(b) // write remainder of line (contains newline)
	}
}

// generateAnchorPoints returns a mapping of *ast.Ident objects to the
// qualified ID that should be set as an anchor point.
func generateAnchorPoints(decl ast.Decl) map[*ast.Ident]string {
	m := map[*ast.Ident]string{}
	switch decl := decl.(type) {
	case *ast.GenDecl:
		for _, sp := range decl.Specs {
			switch decl.Tok {
			case token.CONST, token.VAR:
				for _, name := range sp.(*ast.ValueSpec).Names {
					m[name] = name.Name
				}
			case token.TYPE:
				ts := sp.(*ast.TypeSpec)
				m[ts.Name] = ts.Name.Name

				var fs []*ast.Field
				switch tx := ts.Type.(type) {
				case *ast.StructType:
					fs = tx.Fields.List
				case *ast.InterfaceType:
					fs = tx.Methods.List
				}
				for _, f := range fs {
					for _, id := range f.Names {
						m[id] = ts.Name.String() + "." + id.String()
					}
					if f.Names == nil {
						// Embedded type use the type name as the field name.
						typeName, id := nodeName(f.Type)
						typeName = typeName[strings.LastIndexByte(typeName, '.')+1:]
						m[id] = ts.Name.String() + "." + typeName
					}
				}
			}
		}
	case *ast.FuncDecl:
		anchorID := decl.Name.Name
		if decl.Recv != nil && len(decl.Recv.List) > 0 {
			recvName, _ := nodeName(decl.Recv.List[0].Type)
			recvName = recvName[strings.LastIndexByte(recvName, '.')+1:]
			anchorID = recvName + "." + anchorID
		}
		m[decl.Name] = anchorID
	}
	return m
}

// generateAnchorLinks returns a mapping of *ast.Ident objects to the URL
// that the identifier should link to.
func generateAnchorLinks(idr *identifierResolver, decl ast.Decl) map[*ast.Ident]string {
	m := map[*ast.Ident]string{}
	ignore := map[ast.Node]bool{}
	ast.Inspect(decl, func(node ast.Node) bool {
		if ignore[node] {
			return false
		}
		switch node := node.(type) {
		case *ast.SelectorExpr:
			// Package qualified identifier (e.g., "io.EOF").
			if prefix, _ := node.X.(*ast.Ident); prefix != nil {
				if obj := prefix.Obj; obj != nil && obj.Kind == ast.Pkg {
					if spec, _ := obj.Decl.(*ast.ImportSpec); spec != nil {
						if path, err := strconv.Unquote(spec.Path.Value); err == nil {
							// Register two links, one for the package
							// and one for the qualified identifier.
							m[prefix] = idr.toURL(path, "")
							m[node.Sel] = idr.toURL(path, node.Sel.Name)
							return false
						}
					}
				}
			}
		case *ast.Ident:
			if node.Obj == nil && doc.IsPredeclared(node.Name) {
				m[node] = idr.toURL("builtin", node.Name)
			} else if node.Obj != nil && idr.topLevelDecls[node.Obj.Decl] {
				m[node] = "#" + node.Name
			}
		case *ast.FuncDecl:
			ignore[node.Name] = true // E.g., "func NoLink() int"
		case *ast.TypeSpec:
			ignore[node.Name] = true // E.g., "type NoLink int"
		case *ast.ValueSpec:
			for _, n := range node.Names {
				ignore[n] = true // E.g., "var NoLink1, NoLink2 int"
			}
		case *ast.AssignStmt:
			for _, n := range node.Lhs {
				ignore[n] = true // E.g., "NoLink1, NoLink2 := 0, 1"
			}
		}
		return true
	})
	return m
}
