package source

import (
	"go/ast"
	"go/token"
	"log"
	"strings"
	"unicode"

	"go/types"

	"golang.org/x/tools/internal/span"
)

func (c *completer) getAdditionalTextEdits(pkgPath string) *TextEdit {
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

func (c *completer) init() {
	switch c.path[0].(type) {
	case *ast.Ident, *ast.SelectorExpr:
	default:
		contents := c.file.GetContent(c.ctx)
		tok := c.file.GetToken(c.ctx)
		c.cursorIdent = offsetForIdent(contents, tok.Position(c.pos))
	}
}

func (c *completer) initPrefix() {
	if c.cursorIdent != "" && c.cursorIdent[len(c.cursorIdent)-1] == '.' {
		c.surrounding = &Selection{}
	}
	c.surrounding = &Selection{Content: c.cursorIdent}
}

func (c *completer) scopeVisit(pkgPath, prefix string) {
	if c.search == nil {
		return
	}

	score := stdScore * 2

	f := func(p Package) bool {
		if c.canNotAccess(p.GetTypes().Path()) {
			return false
		}

		if p.GetTypes().Name() == prefix && p.GetTypes().Path() != pkgPath {
			edit := c.getAdditionalTextEdits(p.GetTypes().Path())
			scope := p.GetTypes().Scope()
			for _, name := range scope.Names() {
				l := len(c.items)
				c.found(scope.Lookup(name), score)
				if len(c.items) == l+1 && edit != nil {
					c.items[l].AdditionalTextEdits = append(c.items[l].AdditionalTextEdits, *edit)
				}
			}
		}
		return false
	}

	c.search(f)
}

func (c *completer) packageVisit(prefix string, seen map[string]struct{}) {
	if c.search == nil {
		return
	}

	c.stdModuleVisit(prefix, seen)

	f := func(p Package) bool {
		item := c.createCompletionItem(p.GetTypes().Name(), p.GetTypes().Path(), prefix, seen)
		if item != nil {
			c.items = append(c.items, *item)
		}
		return false
	}

	c.search(f)
}

const internalPkg = "internal"

func (c *completer) canNotAccess(pkgPath string) bool {
	pos := strings.Index(pkgPath, internalPkg)
	if pos == -1 {
		return false
	}

	// "internal/xxx"
	if pos == 0 {
		return true
	}

	pkg := c.file.GetPackage(c.ctx)
	if pkg == nil {
		return false
	}

	// "xxx/internal/xxx"
	dir := pkgPath[:pos-1]
	return !strings.HasPrefix(pkg.GetTypes().Path(), dir)
}

var stdModuleMap = map[string]string{
	"archive/zip": "zip",
	"archive/tar": "tar",

	"bufio":   "bufio",
	"builtin": "builtin",
	"bytes":   "bytes",

	"compress/bzip2": "bzip2",
	"compress/flate": "flate",
	"compress/gzip":  "gzip",
	"compress/lzw":   "lzw",
	"compress/zlib":  "zlib",

	"container/heap": "heap",
	"container/list": "list",
	"container/ring": "ring",

	"context": "context",

	"crypto/aes":       "aes",
	"crypto/cipher":    "cipher",
	"crypto/des":       "des",
	"crypto/dsa":       "dsa",
	"crypto/ecdsa":     "ecdsa",
	"crypto/elliptic":  "elliptic",
	"crypto/hmac":      "hmac",
	"crypto/md5":       "md5",
	"crypto/rand":      "rand",
	"crypto/rc4":       "rc4",
	"crypto/rsa":       "rsa",
	"crypto/sha1":      "sha1",
	"crypto/sha256":    "sha256",
	"crypto/sha512":    "sha512",
	"crypto/subtle":    "subtle",
	"crypto/tls":       "tls",
	"crypto/x509":      "x509",
	"crypto/x509/pkix": "pkix",

	"database/sql":        "sql",
	"database/sql/driver": "driver",

	"debug/dwarf":    "dwarf",
	"debug/elf":      "elf",
	"debug/gosym":    "gosym",
	"debug/macho":    "macho",
	"debug/pe":       "pe",
	"debug/plan9obj": "plan9obj",

	"encoding":         "encoding",
	"encoding/ascii85": "ascii85",
	"encoding/asn1":    "asn1",
	"encoding/base32":  "base32",
	"encoding/base64":  "base64",
	"encoding/binary":  "binary",
	"encoding/csv":     "csv",
	"encoding/gob":     "gob",
	"encoding/hex":     "hex",
	"encoding/json":    "json",
	"encoding/pem":     "pem",
	"encoding/xml":     "xml",

	"errors": "errors",
	"expvar": "expvar",

	"flag": "flag",
	"fmt":  "fmt",

	"go/ast":      "ast",
	"go/build":    "build",
	"go/constant": "constant",
	"go/doc":      "doc",
	"go/format":   "format",
	"go/importer": "importer",
	"go/parser":   "parser",
	"go/printer":  "printer",
	"go/scanner":  "scanner",
	"go/token":    "token",
	"go/types":    "types",

	"hash":         "hash",
	"hash/adler32": "adler32",
	"hash/crc32":   "crc32",
	"hash/crc64":   "crc64",
	"hash/fnv":     "fnv",

	"html":          "html",
	"html/template": "template",

	"image":               "image",
	"image/color":         "color",
	"image/color/palette": "palette",
	"image/draw":          "draw",
	"image/gif":           "gif",
	"image/jpeg":          "jpeg",
	"image/png":           "png",

	"index/suffixarray": "suffixarray",

	"io":        "io",
	"io/ioutil": "ioutil",

	"log":        "log",
	"log/syslog": "syslog",

	"math":       "math",
	"math/big":   "big",
	"math/bits":  "bits",
	"math/cmplx": "cmplx",
	"math/rand":  "rand",

	"mime":                 "mime",
	"mime/multipart":       "multipart",
	"mime/quotedprintable": "quotedprintable",

	"net":                "net",
	"net/http":           "http",
	"net/http/cgi":       "cgi",
	"net/http/cookiejar": "cookiejar",
	"net/http/fcgi":      "fcgi",
	"net/http/httptest":  "httptest",
	"net/http/httptrace": "httptrace",
	"net/http/httputil":  "httputil",
	"net/http/pprof":     "pprof",
	"net/mail":           "mail",
	"net/rpc":            "rpc",
	"net/rpc/jsonrpc":    "jsonrpc",
	"net/smtp":           "smtp",
	"net/textproto":      "textproto",
	"net/url":            "url",

	"os":        "os",
	"os/exec":   "exec",
	"os/signal": "signal",
	"os/user":   "user",

	"path":          "path",
	"path/filepath": "filepath",

	"plugin": "plugin",

	"reflect":       "reflect",
	"regexp":        "regexp",
	"regexp/syntax": "syntax",

	"runtime":       "runtime",
	"runtime/cgo":   "cgo",
	"runtime/debug": "debug",
	"runtime/msan":  "msan",
	"runtime/pprof": "pprof",
	"runtime/race":  "race",
	"runtime/trace": "trace",

	"sort":        "sort",
	"strconv":     "strconv",
	"strings":     "strings",
	"sync":        "sync",
	"sync/atomic": "atomic",
	"syscall":     "syscall",
	"syscall/js":  "js",

	"testing":        "testing",
	"testing/iotest": "iotest",
	"testing/quick":  "quick",

	"text/scanner":        "scanner",
	"text/tabwriter":      "tabwriter",
	"text/template":       "template",
	"text/template/parse": "parse",

	"time": "time",

	"unicode":       "unicode",
	"unicode/utf16": "utf16",
	"unicode/utf8":  "utf8",

	"unsafe": "unsafe",
}

func (c *completer) stdModuleVisit(prefix string, seen map[string]struct{}) {
	for path, name := range stdModuleMap {
		item := c.createCompletionItem(name, path, prefix, seen)
		if item != nil {
			c.items = append(c.items, *item)
		}
	}
}

func (c *completer) createCompletionItem(pkgName string, pkgPath string, prefix string, seen map[string]struct{}) *CompletionItem {
	if c.canNotAccess(pkgPath) {
		return nil
	}

	if _, ok := seen[pkgPath]; ok {
		return nil
	}
	seen[pkgPath] = struct{}{}

	if !strings.HasPrefix(pkgName, prefix) {
		return nil
	}

	score := stdScore * 2

	item := &CompletionItem{
		Label:      pkgName,
		Detail:     pkgPath,
		InsertText: pkgName,
		Kind:       PackageCompletionItem,
		Score:      score,
	}
	edit := c.getAdditionalTextEdits(pkgPath)
	if edit != nil {
		item.AdditionalTextEdits = append(item.AdditionalTextEdits, *edit)
	}

	return item
}

func (c *completer) closure(obj types.Object, score float64) bool {
	items := c.items
	c.items = c.items[0:0]
	if obj.Name()+"." == c.cursorIdent && obj.Type() != types.Typ[types.Invalid] {
		objType := obj.Type()
		// methods of T
		mset := types.NewMethodSet(objType)
		for i := 0; i < mset.Len(); i++ {
			c.found(mset.At(i).Obj(), score)
		}

		// methods of *T
		if !types.IsInterface(obj.Type()) && !isPointer(objType) {
			mset := types.NewMethodSet(types.NewPointer(objType))
			for i := 0; i < mset.Len(); i++ {
				c.found(mset.At(i).Obj(), score)
			}
		}

		// fields of T
		for _, f := range fieldSelections(objType) {
			c.found(f, score)
		}
	}

	if len(c.items) > 0 {
		return true
	}

	c.items = items
	return false
}

func (c *completer) globalCompletion(seen map[string]struct{}) {
	ident := c.cursorIdent
	if ident == "" {
		if id, ok := c.path[0].(*ast.Ident); ok {
			c.packageVisit(id.Name, seen)
		}
		return
	}

	l := len(ident)
	if ident[l-1] == '.' {
		c.scopeVisit(c.types.Path(), ident[:l-1])
	} else {
		c.packageVisit(ident, seen)
	}
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
