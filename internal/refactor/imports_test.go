// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package refactor_test

import (
	"fmt"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"runtime"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"golang.org/x/tools/internal/refactor"
	"golang.org/x/tools/internal/testenv"
)

func TestAddImport(t *testing.T) {
	testenv.NeedsDefaultImporter(t)

	descr := func(s string) string {
		if _, _, line, ok := runtime.Caller(1); ok {
			return fmt.Sprintf("L%d %s", line, s)
		}
		panic("runtime.Caller failed")
	}

	// Each test case contains a «name pkgpath member»
	// triple to be replaced with a valid qualified identifier
	// to pkgpath.member, ideally of the specified name.
	for _, test := range []struct {
		descr, src, want string
	}{
		{
			descr: descr("simple add import"),
			src: `package a
func _() {
	«fmt fmt Print»
}`,
			want: `package a
import "fmt"

func _() {
	fmt.Print
}`,
		},
		{
			descr: descr("existing import"),
			src: `package a

import "fmt"

func _(fmt.Stringer) {
	«fmt fmt Print»
}`,
			want: `package a

import "fmt"

func _(fmt.Stringer) {
	fmt.Print
}`,
		},
		{
			descr: descr("existing blank import"),
			src: `package a

import _ "fmt"

func _() {
	«fmt fmt Print»
}`,
			want: `package a

import "fmt"

import _ "fmt"

func _() {
	fmt.Print
}`,
		},
		{
			descr: descr("existing renaming import"),
			src: `package a

import fmtpkg "fmt"

var fmt int

func _(fmtpkg.Stringer) {
	«fmt fmt Print»
}`,
			want: `package a

import fmtpkg "fmt"

var fmt int

func _(fmtpkg.Stringer) {
	fmtpkg.Print
}`,
		},
		{
			descr: descr("existing import is shadowed"),
			src: `package a

import "fmt"

var _ fmt.Stringer

func _(fmt int) {
	«fmt fmt Print»
}`,
			want: `package a

import fmt0 "fmt"

import "fmt"

var _ fmt.Stringer

func _(fmt int) {
	fmt0.Print
}`,
		},
		{
			descr: descr("preferred name is shadowed"),
			src: `package a

import "fmt"

func _(fmt fmt.Stringer) {
	«fmt fmt Print»
}`,
			want: `package a

import fmt0 "fmt"

import "fmt"

func _(fmt fmt.Stringer) {
	fmt0.Print
}`,
		},
		{
			descr: descr("import inserted before doc comments"),
			src: `package a

// hello
import "os"

// world
func _() {
	«fmt fmt Print»
}`,
			want: `package a

import "fmt"

// hello
import "os"

// world
func _() {
	fmt.Print
}`,
		},
		{
			descr: descr("arbitrary preferred name => renaming import"),
			src: `package a

func _() {
	«foo encoding/json Marshal»
}`,
			want: `package a

import foo "encoding/json"

func _() {
	foo.Marshal
}`,
		},
		{
			descr: descr("dot import unshadowed"),
			src: `package a

import . "fmt"

func _() {
	«. fmt Print»
}`,
			want: `package a

import . "fmt"

func _() {
	Print
}`,
		},
		{
			descr: descr("dot import shadowed"),
			src: `package a

import . "fmt"

func _(Print fmt.Stringer) {
	«fmt fmt Print»
}`,
			want: `package a

import "fmt"

import . "fmt"

func _(Print fmt.Stringer) {
	fmt.Print
}`,
		},
		{
			descr: descr("add import to group"),
			src: `package a

import (
	"io"
)

func _(io.Reader) {
	«fmt fmt Print»
}`,
			want: `package a

import (
	"fmt"
	"io"
)

func _(io.Reader) {
	fmt.Print
}`,
		},
		{
			descr: descr("add import to group which imports std and a 3rd module"),
			src: `package a

import (
	"io"

	"vendor/golang.org/x/net/dns/dnsmessage"
)

func _(io.Reader) {
	«fmt fmt Print»
}`,
			want: `package a

import (
	"fmt"
	"io"

	"vendor/golang.org/x/net/dns/dnsmessage"
)

func _(io.Reader) {
	fmt.Print
}`,
		},
		{
			descr: descr("add import to group which imports std and a 3rd module without parens"),
			src: `package a

import "io"

import "vendor/golang.org/x/net/dns/dnsmessage"

func _(io.Reader) {
	«fmt fmt Print»
}`,
			want: `package a

import "fmt"

import "io"

import "vendor/golang.org/x/net/dns/dnsmessage"

func _(io.Reader) {
	fmt.Print
}`,
		},
		{
			descr: descr("add import to group without std import"),
			src: `package a

import (
	"golang.org/x/tools/go/packages"
	gossa "golang.org/x/tools/go/ssa"
)

func _(io.Reader) {
	«fmt fmt Print»
}`,
			want: `package a

import (
	"fmt"

	"golang.org/x/tools/go/packages"
	gossa "golang.org/x/tools/go/ssa"
)

func _(io.Reader) {
	fmt.Print
}`,
		},
	} {
		t.Run(test.descr, func(t *testing.T) {
			// splice marker (name pkgpath member)
			before, mid, ok1 := strings.Cut(test.src, "«")
			mid, after, ok2 := strings.Cut(mid, "»")
			if !ok1 || !ok2 {
				t.Fatal("no «name path member» marker")
			}
			src := before + "/*!*/" + after
			fields := strings.Fields(mid)
			if len(fields) != 3 {
				t.Fatalf("splice marker needs 3 fields (got %q)", mid)
			}
			name, path, member := fields[0], fields[1], fields[2]

			// parse
			fset := token.NewFileSet()
			f, err := parser.ParseFile(fset, "a.go", src, parser.ParseComments)
			if err != nil {
				t.Log(err)
			}
			pos := fset.File(f.FileStart).Pos(len(before))

			// type-check
			info := &types.Info{
				Types:     make(map[ast.Expr]types.TypeAndValue),
				Scopes:    make(map[ast.Node]*types.Scope),
				Defs:      make(map[*ast.Ident]types.Object),
				Implicits: make(map[ast.Node]types.Object),
			}
			conf := &types.Config{
				// We don't want to fail if there is an error during type checking:
				// the error may be because we're missing an import, and adding imports
				// is the whole point of AddImport.
				Error:    func(err error) { t.Log(err) },
				Importer: importer.Default(),
			}
			conf.Check(f.Name.Name, fset, []*ast.File{f}, info)

			prefix, edits := refactor.AddImport(info, f, name, path, member, pos)

			var edit refactor.Edit
			switch len(edits) {
			case 0:
			case 1:
				edit = edits[0]
			default:
				t.Fatalf("expected at most one edit, got %d", len(edits))
			}

			// apply patch
			start := fset.Position(edit.Pos)
			end := fset.Position(edit.End)
			output := src[:start.Offset] + string(edit.NewText) + src[end.Offset:]
			output = strings.ReplaceAll(output, "/*!*/", prefix+member)
			if output != test.want {
				t.Errorf("\n--got--\n%s\n--want--\n%s\n--diff--\n%s",
					output, test.want, cmp.Diff(test.want, output))
			}
		})
	}
}
