// Copyright 2017 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package render

import (
	"fmt"
	"go/parser"
	"go/token"
	"testing"
)

func TestOneLineNode(t *testing.T) {
	src := `
		package insane

		import ()
		import ("io")
		import "io"
		import (
			io "io"
		)
		import (
			"io"
			"fmt"
		)

		type private int

		func (p *private) Method1() string { return "" }

		func Foo(ctx Context, s struct {
			Fizz struct {
				Field int
			}
			Buzz interface {
				Method() int
			}
		}) (_ private) {
			return
		}

		var bytes struct{ Buffer int }

		var Var = func(i int) io.Reader {
			// Comment
			var tar struct{ Header int }
			_ = time.Time{}  // Comment
			_ = bytes.Buffer // Comment
			_ = tar.Header   // Comment
			return nil
		}(1000)

		var Var2, _ = io.Copy(io.Writer(nil), &io.LimitedReader{nil, 0})

		var Var3 = NewStruct2()

		var EOF = io.EOF

		var Var4 = EOF

		var (
			x1 = "unexported"
			// fafewa
			X2 = "exported"
		)

		type Struct struct {
			// Some commment

			// Another comment.
			Field int

			Struct1, Struct2 (***(***struct {
				Func func(struct {
					Struct struct {
						Struct struct{ Field int }
					}
				})
			}))

			Iface interface {
				Method()
			}
		}

		type Iface interface{
			Method()
		}

		type CrazyIface **(**(interface {
			io.Reader
			Method(interface {
				Method(struct {
					Field int
				})
			})
		}))

		type EmptyStruct struct{}
		type EmptyIface interface{}

		type ()
		var ()
		const ()

		type (
			Foo struct{x  int}
			Bar string
		)

		func (s *Struct2) Method() {}

		const (
			C1, C2, C3 = 1, 2, 3
		)

		type Node struct {
			Next *Node
		}

		func NewStruct2() *Struct2 {
			return nil
		}

		var (
			Large1 = []int{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}
			Large2 = []int{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}
		)`
	want := []string{
		`import ()`,
		`import "io"`,
		`import "io"`,
		`import "io"`,
		`import "io" ...`,
		`type private int`,
		`func (p *private) Method1() string`,
		`func Foo(ctx Context, s struct{ ... }) (_ private)`,
		`var bytes struct{ ... }`,
		`var Var = func(i int) io.Reader { ... }(1000)`,
		`var Var2 = io.Copy(io.Writer(nil), &io.LimitedReader{ ... }) ...`,
		`var Var3 = NewStruct2()`,
		`var EOF = io.EOF`,
		`var Var4 = EOF`,
		`var x1 = "unexported" ...`,
		`type Struct struct{ ... }`,
		`type Iface interface{ ... }`,
		`type CrazyIface ...`,
		`type EmptyStruct struct{}`,
		`type EmptyIface interface{}`,
		`type ()`,
		`var ()`,
		`const ()`,
		`type Foo struct{ ... } ...`,
		`func (s *Struct2) Method()`,
		`const C1 = 1 ...`,
		`type Node struct{ ... }`,
		`func NewStruct2() *Struct2`,
		`var Large1 = []int{ ... } ...`,
	}

	// Parse src but stop after processing the imports.
	fset := token.NewFileSet() // positions are relative to fset
	f, err := parser.ParseFile(fset, "", src, parser.ParseComments)
	if err != nil {
		fmt.Println(err)
		return
	}

	// Print the imports from the file's AST.
	for i, d := range f.Decls {
		got := oneLineNodeDepth(fset, d, 10)
		if got != want[i] {
			t.Errorf("test %d, oneLineNode():\ngot  %s\nwant %s", i, got, want[i])
		}
	}
}
