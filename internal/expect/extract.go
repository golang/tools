// Copyright 2018 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package expect

import (
	"fmt"
	"go/ast"
	"go/parser"
	goscanner "go/scanner"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"text/scanner"

	"golang.org/x/mod/modfile"
)

const commentStart = "@"
const commentStartLen = len(commentStart)

// Identifier is the type for an identifier in a Note argument list.
type Identifier string

// Parse collects all the notes present in a file.
// If content is nil, the filename specified is read and parsed, otherwise the
// content is used and the filename is used for positions and error messages.
// Each comment whose text starts with @ is parsed as a comma-separated
// sequence of notes.
// See the package documentation for details about the syntax of those
// notes.
func Parse(fset *token.FileSet, filename string, content []byte) ([]*Note, error) {
	if content == nil {
		data, err := os.ReadFile(filename)
		if err != nil {
			return nil, err
		}
		content = data
	}

	switch filepath.Ext(filename) {
	case ".s":
		// The assembler uses a custom scanner,
		// but the go/scanner package is close
		// enough: we only want the comments.
		file := fset.AddFile(filename, -1, len(content))
		var scan goscanner.Scanner
		scan.Init(file, content, nil, goscanner.ScanComments)

		var notes []*Note
		for {
			pos, tok, lit := scan.Scan()
			if tok == token.EOF {
				break
			}
			if tok == token.COMMENT {
				text, adjust := getAdjustedNote(lit)
				if text == "" {
					continue
				}
				parsed, err := parse(fset, pos+token.Pos(adjust), text)
				if err != nil {
					return nil, err
				}
				notes = append(notes, parsed...)
			}
		}
		return notes, nil

	case ".go":
		// TODO: We should write this in terms of the scanner, like the .s case above.
		// there are ways you can break the parser such that it will not add all the
		// comments to the ast, which may result in files where the tests are silently
		// not run.
		file, err := parser.ParseFile(fset, filename, content, parser.ParseComments|parser.AllErrors|parser.SkipObjectResolution)
		if file == nil {
			return nil, err
		}
		return ExtractGo(fset, file)

	case ".mod":
		file, err := modfile.Parse(filename, content, nil)
		if err != nil {
			return nil, err
		}
		f := fset.AddFile(filename, -1, len(content))
		f.SetLinesForContent(content)
		notes, err := extractModWork(fset, file.Syntax.Stmt)
		if err != nil {
			return nil, err
		}
		// Since modfile.Parse does not return an *ast, we need to add the offset
		// within the file's contents to the file's base relative to the fileset.
		for _, note := range notes {
			note.Pos += token.Pos(f.Base())
		}
		return notes, nil

	case ".work":
		file, err := modfile.ParseWork(filename, content, nil)
		if err != nil {
			return nil, err
		}
		f := fset.AddFile(filename, -1, len(content))
		f.SetLinesForContent(content)
		notes, err := extractModWork(fset, file.Syntax.Stmt)
		if err != nil {
			return nil, err
		}
		// As with go.mod files, we need to compute a synthetic token.Pos.
		for _, note := range notes {
			note.Pos += token.Pos(f.Base())
		}
		return notes, nil
	}
	return nil, nil
}

// extractModWork collects all the notes present in a go.mod file or go.work
// file, by way of the shared modfile.Expr statement node.
//
// Each comment whose text starts with @ is parsed as a comma-separated
// sequence of notes.
// See the package documentation for details about the syntax of those
// notes.
// Only allow notes to appear with the following format: "//@mark()" or // @mark()
func extractModWork(fset *token.FileSet, exprs []modfile.Expr) ([]*Note, error) {
	var notes []*Note
	for _, stmt := range exprs {
		comment := stmt.Comment()
		if comment == nil {
			continue
		}
		var allComments []modfile.Comment
		allComments = append(allComments, comment.Before...)
		allComments = append(allComments, comment.Suffix...)
		for _, cmt := range allComments {
			text, adjust := getAdjustedNote(cmt.Token)
			if text == "" {
				continue
			}
			parsed, err := parse(fset, token.Pos(int(cmt.Start.Byte)+adjust), text)
			if err != nil {
				return nil, err
			}
			notes = append(notes, parsed...)
		}
	}
	return notes, nil
}

// ExtractGo collects all the notes present in an AST.
// Each comment whose text starts with @ is parsed as a comma-separated
// sequence of notes.
// See the package documentation for details about the syntax of those
// notes.
func ExtractGo(fset *token.FileSet, file *ast.File) ([]*Note, error) {
	var notes []*Note
	for _, g := range file.Comments {
		for _, c := range g.List {
			text, adjust := getAdjustedNote(c.Text)
			if text == "" {
				continue
			}
			parsed, err := parse(fset, token.Pos(int(c.Pos())+adjust), text)
			if err != nil {
				return nil, err
			}
			notes = append(notes, parsed...)
		}
	}
	return notes, nil
}

func getAdjustedNote(text string) (string, int) {
	if strings.HasPrefix(text, "/*") {
		text = strings.TrimSuffix(text, "*/")
	}
	text = text[2:] // remove "//" or "/*" prefix

	// Allow notes to appear within comments.
	// For example:
	// "// //@mark()" is valid.
	// "// @mark()" is not valid.
	// "// /*@mark()*/" is not valid.
	var adjust int
	if i := strings.Index(text, commentStart); i > 2 {
		// Get the text before the commentStart.
		pre := text[i-2 : i]
		if pre != "//" {
			return "", 0
		}
		text = text[i:]
		adjust = i
	}
	if !strings.HasPrefix(text, commentStart) {
		return "", 0
	}
	text = text[commentStartLen:]
	return text, commentStartLen + adjust + 1
}

const invalidToken rune = 0

type tokens struct {
	scanner scanner.Scanner
	current rune
	err     error
	base    token.Pos
}

func (t *tokens) Init(base token.Pos, text string) *tokens {
	t.base = base
	t.scanner.Init(strings.NewReader(text))
	t.scanner.Mode = scanner.GoTokens
	t.scanner.Whitespace ^= 1 << '\n' // don't skip new lines
	t.scanner.Error = func(s *scanner.Scanner, msg string) {
		t.Errorf("%v", msg)
	}
	return t
}

func (t *tokens) Consume() string {
	t.current = invalidToken
	return t.scanner.TokenText()
}

func (t *tokens) Token() rune {
	if t.err != nil {
		return scanner.EOF
	}
	if t.current == invalidToken {
		t.current = t.scanner.Scan()
	}
	return t.current
}

func (t *tokens) Skip(r rune) int {
	i := 0
	for t.Token() == '\n' {
		t.Consume()
		i++
	}
	return i
}

func (t *tokens) TokenString() string {
	return scanner.TokenString(t.Token())
}

func (t *tokens) Pos() token.Pos {
	return t.base + token.Pos(t.scanner.Position.Offset)
}

func (t *tokens) Errorf(msg string, args ...any) {
	if t.err != nil {
		return
	}
	t.err = fmt.Errorf(msg, args...)
}

func parse(fset *token.FileSet, base token.Pos, text string) ([]*Note, error) {
	t := new(tokens).Init(base, text)
	notes := parseComment(t)
	if t.err != nil {
		return nil, fmt.Errorf("%v: %s", fset.Position(t.Pos()), t.err)
	}
	return notes, nil
}

func parseComment(t *tokens) []*Note {
	var notes []*Note
	for {
		t.Skip('\n')
		switch t.Token() {
		case scanner.EOF:
			return notes
		case scanner.Ident:
			notes = append(notes, parseNote(t))
		default:
			t.Errorf("unexpected %s parsing comment, expect identifier", t.TokenString())
			return nil
		}
		switch t.Token() {
		case scanner.EOF:
			return notes
		case ',', '\n':
			t.Consume()
		default:
			t.Errorf("unexpected %s parsing comment, expect separator", t.TokenString())
			return nil
		}
	}
}

func parseNote(t *tokens) *Note {
	n := &Note{
		Pos:  t.Pos(),
		Name: t.Consume(),
	}

	switch t.Token() {
	case ',', '\n', scanner.EOF:
		// no argument list present
		return n
	case '(':
		n.Args, n.NamedArgs = parseArgumentList(t)
		return n
	default:
		t.Errorf("unexpected %s parsing note", t.TokenString())
		return nil
	}
}

func parseArgumentList(t *tokens) (args []any, named map[string]any) {
	args = []any{} // @name() is represented by a non-nil empty slice.
	t.Consume()    // '('
	t.Skip('\n')
	for t.Token() != ')' {
		name, arg := parseArgument(t)
		if name != "" {
			// f(k=v)
			if named == nil {
				named = make(map[string]any)
			}
			if _, dup := named[name]; dup {
				t.Errorf("duplicate named argument %q", name)
				return nil, nil
			}
			named[name] = arg
		} else {
			// f(v)
			if named != nil {
				t.Errorf("positional argument follows named argument")
				return nil, nil
			}
			args = append(args, arg)
		}
		if t.Token() != ',' {
			break
		}
		t.Consume()
		t.Skip('\n')
	}
	if t.Token() != ')' {
		t.Errorf("unexpected %s parsing argument list", t.TokenString())
		return nil, nil
	}
	t.Consume() // ')'
	return args, named
}

// parseArgument returns the value of the argument ("f(value)"),
// and its name if named "f(name=value)".
func parseArgument(t *tokens) (name string, value any) {
again:
	switch t.Token() {
	case scanner.Ident:
		v := t.Consume()
		switch v {
		case "true":
			value = true
		case "false":
			value = false
		case "nil":
			value = nil
		case "re":
			if t.Token() != scanner.String && t.Token() != scanner.RawString {
				t.Errorf("re must be followed by string, got %s", t.TokenString())
				return
			}
			pattern, _ := strconv.Unquote(t.Consume()) // can't fail
			re, err := regexp.Compile(pattern)
			if err != nil {
				t.Errorf("invalid regular expression %s: %v", pattern, err)
				return
			}
			value = re
		default:
			// f(name=value)?
			if name == "" && t.Token() == '=' {
				t.Consume() // '='
				name = v
				goto again
			}
			value = Identifier(v)
		}

	case scanner.String, scanner.RawString:
		value, _ = strconv.Unquote(t.Consume()) // can't fail

	case scanner.Int:
		s := t.Consume()
		v, err := strconv.ParseInt(s, 0, 0)
		if err != nil {
			t.Errorf("cannot convert %v to int: %v", s, err)
		}
		value = v

	case scanner.Float:
		s := t.Consume()
		v, err := strconv.ParseFloat(s, 64)
		if err != nil {
			t.Errorf("cannot convert %v to float: %v", s, err)
		}
		value = v

	case scanner.Char:
		t.Errorf("unexpected char literal %s", t.Consume())

	default:
		t.Errorf("unexpected %s parsing argument", t.TokenString())
	}
	return
}
