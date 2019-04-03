package source

import (
	"context"
	"go/ast"
	"go/token"
	"log"
	"strings"
	"unicode"

	"golang.org/x/tools/internal/span"
)

type CompletionHelper struct {
	ctx         context.Context
	file        File
	path        []ast.Node
	cursorIdent string
	search      SearchFunc
}

func newCompletionHelper(ctx context.Context, file File, path []ast.Node, search SearchFunc) *CompletionHelper {
	return &CompletionHelper{ctx: ctx, file: file, path: path, search: search}
}

func (c *CompletionHelper) GetAdditionalTextEdits(pkgPath string) *TextEdit {
	l := len(c.path)
	if l == 0 {
		return nil
	}

	f, ok := c.path[l-1].(*ast.File)
	if !ok {
		return nil
	}

	newText := `"` + pkgPath + `"`
	for _, imp := range f.Imports {
		if imp.Path.Value == newText {
			return nil
		}
	}

	l = len(f.Imports)
	var pos token.Pos
	if l == 0 {
		pos = f.Name.NamePos + token.Pos(len(f.Name.Name))
		newText = "\n\nimport(\n\t" + newText + "\n)"
	} else {
		p := f.Imports[l-1].Path
		pos = p.ValuePos + token.Pos(len(p.Value))
		newText = "\n\t" + newText
	}

	point := toPoint(c.file.GetFileSet(c.ctx), pos)
	return &TextEdit{
		Span:    span.New(c.file.URI(), point, point),
		NewText: newText,
	}
}

func (c *CompletionHelper) initCursorIdent(pos token.Pos) {
	contents := c.file.GetContent(c.ctx)
	tok := c.file.GetToken(c.ctx)
	c.cursorIdent = offsetForIdent(contents, tok.Position(pos))
}

func (c *CompletionHelper) Prefix() string {
	if c.cursorIdent != "" && c.cursorIdent[len(c.cursorIdent)-1] == '.' {
		return ""
	}
	return c.cursorIdent
}

func (c *CompletionHelper) CursorIdent() string {
	return c.cursorIdent
}

func (c *CompletionHelper) ScopeVisit(pkgPath, prefix string, found finder) (items []CompletionItem) {
	score := stdScore * 2
	f := func(p Package) bool {
		if p.GetTypes().Name() == prefix && p.GetTypes().Path() != pkgPath {
			edit := c.GetAdditionalTextEdits(p.GetTypes().Path())
			scope := p.GetTypes().Scope()
			for _, name := range scope.Names() {
				l := len(items)
				items = found(scope.Lookup(name), score, items)
				if len(items) == l+1 && edit != nil {
					items[l].AdditionalTextEdits = append(items[l].AdditionalTextEdits, *edit)
				}
			}
		}
		return false
	}

	c.search(f)
	return items
}

func (c *CompletionHelper) PackageVisit(prefix string) (items []CompletionItem) {
	seen := map[string]struct{}{}

	items = c.stdModuleVisit(prefix, items, seen)

	f := func(p Package) bool {
		item := c.createCompletionItem(p.GetTypes().Name(), p.GetTypes().Path(), prefix, seen)
		if item != nil {
			items = append(items, *item)
		}
		return false
	}
	c.search(f)

	return items
}

var stdModuleMap = map[string]string{
	"archive/zip": "zip",
	"archive/tar": "tar",
	"bufio":       "bufio",
	"bytes":       "bytes",
	"context":     "context",
	"errors":      "errors",
	"fmt":         "fmt",
	"io":          "io",
	"io/ioutil":   "ioutil",
	"go/ast":      "ast",
	"go/token":    "token",
	"math":        "math",
	"math/rand":   "rand",
	"net/http":    "http",

	"os":        "os",
	"os/signal": "signal",
	"os/exec":   "exec",

	"path":          "path",
	"path/filepath": "filepath",

	"regexp":        "regexp",
	"runtime/pprof": "pprof",

	"sort":    "sort",
	"strconv": "strconv",
	"strings": "strings",

	"time":   "time",
	"unsafe": "unsafe",
}

func (c *CompletionHelper) stdModuleVisit(prefix string, items []CompletionItem, seen map[string]struct{}) []CompletionItem {
	for path, name := range stdModuleMap {
		item := c.createCompletionItem(name, path, prefix, seen)
		if item != nil {
			items = append(items, *item)
		}
	}
	return items
}

func (c *CompletionHelper) createCompletionItem(pkgName string, pkgPath string, prefix string, seen map[string]struct{}) *CompletionItem {
	if _, ok := seen[pkgPath]; ok {
		return nil
	}
	seen[pkgName] = struct{}{}

	if !strings.HasPrefix(pkgName, prefix) {
		return nil
	}

	score := stdScore * 2

	item := &CompletionItem{
		Label:  pkgName,
		Detail: pkgPath,
		Kind:   PackageCompletionItem,
		Score:  score,
	}
	edit := c.GetAdditionalTextEdits(pkgPath)
	if edit != nil {
		item.AdditionalTextEdits = append(item.AdditionalTextEdits, *edit)
	}

	return item
}

func toPoint(fSet *token.FileSet, pos token.Pos) span.Point {
	p := fSet.Position(pos)
	return span.NewPoint(p.Line, p.Column, p.Offset)
}

func offsetForIdent(contents []byte, p token.Position) string {
	p.Line--
	p.Column--

	line := 0
	col := 0

	offset := 0
	size := 0
	s := string(contents)
	for i, b := range s {
		if line == p.Line && col == p.Column {
			break
		}
		if (line == p.Line && col > p.Column) || line > p.Line {
			log.Printf("character %d is beyond line %d boundary", p.Column, p.Line)
			return ""
		}
		size = len(string(b))
		offset = i + size
		if b == '\n' {
			line++
			col = 0
		} else {
			col++
		}
	}

	if line == p.Line && col == p.Column {
		prefix := contents[:offset]
		i := offset - 1
		for ; i > 0; i-- {
			c := rune(prefix[i])
			if unicode.IsLetter(c) || c == '.' || unicode.IsDigit(c) {
				continue
			}
			break
		}
		result := string(contents[i+1 : offset])
		return result
	}

	if line == 0 {
		log.Printf("character %d is beyond first line boundary", p.Column)
		return ""
	}

	log.Printf("file only has %d lines", line+1)
	return ""
}
