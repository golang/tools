// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package golang

import (
	"fmt"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

// TestFreeRefs is a unit test of the free-references algorithm.
func TestFreeRefs(t *testing.T) {
	if runtime.GOOS == "js" {
		t.Skip("some test imports are unsupported on js")
	}

	for i, test := range []struct {
		src  string
		want []string // expected list of "scope kind dotted-path" triples
	}{
		{
			// basic example (has a "cannot infer" type error)
			`package p; func f[T ~int](x any) { var y T; « f(x.(T) + y) » }`,
			[]string{"pkg func f", "local var x", "local typename T", "local var y"},
		},
		{
			// selection need not be tree-aligned
			`package p; type T int; type U « T; func _(x U) »`,
			[]string{"pkg typename T", "pkg typename U"},
		},
		{
			// imported symbols
			`package p; import "fmt"; func f() { « var x fmt.Stringer » }`,
			[]string{"file pkgname fmt.Stringer"},
		},
		{
			// unsafe and error, our old nemeses
			`package p; import "unsafe"; var ( « _  unsafe.Pointer; _ = error(nil).Error »; )`,
			[]string{"file pkgname unsafe.Pointer"},
		},
		{
			// two attributes of a var, but not the var itself
			`package p; import "bytes"; func _(buf bytes.Buffer) { « buf.WriteByte(0); buf.WriteString(""); » }`,
			[]string{"local var buf.WriteByte", "local var buf.WriteString"},
		},
		{
			// dot imports (an edge case)
			`package p; import . "errors"; var _ = « New»`,
			[]string{"file pkgname errors.New"},
		},
		{
			// struct field (regression test for overzealous dot import logic)
			`package p; import "net/url"; var _ = «url.URL{Host: ""}»`,
			[]string{"file pkgname url.URL"},
		},
		{
			// dot imports (another regression test of same)
			`package p; import . "net/url"; var _ = «URL{Host: ""}»`,
			[]string{"file pkgname url.URL"},
		},
		{
			// dot import of unsafe (a corner case)
			`package p; import . "unsafe"; var _ « Pointer»`,
			[]string{"file pkgname unsafe.Pointer"},
		},
		{
			// dotted path
			`package p; import "go/build"; var _ = « build.Default.GOOS »`,
			[]string{"file pkgname build.Default.GOOS"},
		},
		{
			// type error
			`package p; import "nope"; var _ = « nope.nope.nope »`,
			[]string{"file pkgname nope"},
		},
	} {
		name := fmt.Sprintf("file%d.go", i)
		t.Run(name, func(t *testing.T) {
			fset := token.NewFileSet()
			startOffset := strings.Index(test.src, "«")
			endOffset := strings.Index(test.src, "»")
			if startOffset < 0 || endOffset < startOffset {
				t.Fatalf("invalid «...» selection (%d:%d)", startOffset, endOffset)
			}
			src := test.src[:startOffset] +
				" " +
				test.src[startOffset+len("«"):endOffset] +
				" " +
				test.src[endOffset+len("»"):]
			f, err := parser.ParseFile(fset, name, src, 0)
			if err != nil {
				t.Fatal(err)
			}
			conf := &types.Config{
				Importer: importer.Default(),
				Error:    func(err error) { t.Log(err) }, // not fatal
			}
			info := &types.Info{
				Uses:   make(map[*ast.Ident]types.Object),
				Scopes: make(map[ast.Node]*types.Scope),
				Types:  make(map[ast.Expr]types.TypeAndValue),
			}
			pkg, _ := conf.Check(f.Name.Name, fset, []*ast.File{f}, info) // ignore errors
			tf := fset.File(f.Package)
			refs := freeRefs(pkg, info, f, tf.Pos(startOffset), tf.Pos(endOffset))

			kind := func(obj types.Object) string { // e.g. "var", "const"
				return strings.ToLower(reflect.TypeOf(obj).Elem().Name())
			}

			var got []string
			for _, ref := range refs {
				msg := ref.scope + " " + kind(ref.objects[0]) + " " + ref.dotted
				got = append(got, msg)
			}
			if diff := cmp.Diff(test.want, got); diff != "" {
				t.Errorf("(-want +got)\n%s", diff)
			}
		})
	}
}
