// Copyright 2021 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package completion

import (
	"strings"
	"testing"

	. "golang.org/x/tools/gopls/internal/test/integration"
)

func TestPostfixSnippetCompletion(t *testing.T) {
	const mod = `
-- go.mod --
module mod.com

go 1.12
`

	cases := []struct {
		name              string
		before, after     string
		allowMultipleItem bool
	}{
		{
			name: "sort",
			before: `
package foo

func _() {
	var foo []int
	foo.sort
}
`,
			after: `
package foo

import "sort"

func _() {
	var foo []int
	sort.Slice(foo, func(i, j int) bool {
	$0
})
}
`,
		},
		{
			name: "sort_renamed_sort_package",
			before: `
package foo

import blahsort "sort"

var j int

func _() {
	var foo []int
	foo.sort
}
`,
			after: `
package foo

import blahsort "sort"

var j int

func _() {
	var foo []int
	blahsort.Slice(foo, func(i, j2 int) bool {
	$0
})
}
`,
		},
		{
			name: "last",
			before: `
package foo

func _() {
	var s struct { i []int }
	s.i.last
}
`,
			after: `
package foo

func _() {
	var s struct { i []int }
	s.i[len(s.i)-1]
}
`,
		},
		{
			name: "reverse",
			before: `
package foo

func _() {
	var foo []int
	foo.reverse
}
`,
			after: `
package foo

func _() {
	var foo []int
	for i, j := 0, len(foo)-1; i < j; i, j = i+1, j-1 {
	foo[i], foo[j] = foo[j], foo[i]
}

}
`,
		},
		{
			name: "slice_range",
			before: `
package foo

func _() {
	type myThing struct{}
	var foo []myThing
	foo.range
}
`,
			after: `
package foo

func _() {
	type myThing struct{}
	var foo []myThing
	for ${1:}, ${2:} := range foo {
	$0
}
}
`,
		},
		{
			name: "append_stmt",
			before: `
package foo

func _() {
	var foo []int
	foo.append
}
`,
			after: `
package foo

func _() {
	var foo []int
	foo = append(foo, $0)
}
`,
		},
		{
			name: "append_expr",
			before: `
package foo

func _() {
	var foo []int
	var _ []int = foo.append
}
`,
			after: `
package foo

func _() {
	var foo []int
	var _ []int = append(foo, $0)
}
`,
		},
		{
			name: "slice_copy",
			before: `
package foo

func _() {
	var foo []int
	foo.copy
}
`,
			after: `
package foo

func _() {
	var foo []int
	fooCopy := make([]int, len(foo))
copy(fooCopy, foo)

}
`,
		},
		{
			name: "map_range",
			before: `
package foo

func _() {
	var foo map[string]int
	foo.range
}
`,
			after: `
package foo

func _() {
	var foo map[string]int
	for ${1:}, ${2:} := range foo {
	$0
}
}
`,
		},
		{
			name: "map_clear",
			before: `
package foo

func _() {
	var foo map[string]int
	foo.clear
}
`,
			after: `
package foo

func _() {
	var foo map[string]int
	for k := range foo {
	delete(foo, k)
}

}
`,
		},
		{
			name: "map_keys",
			before: `
package foo

func _() {
	var foo map[string]int
	foo.keys
}
`,
			after: `
package foo

func _() {
	var foo map[string]int
	keys := make([]string, 0, len(foo))
for k := range foo {
	keys = append(keys, k)
}

}
`,
		},
		{
			name: "channel_range",
			before: `
package foo

func _() {
	foo := make(chan int)
	foo.range
}
`,
			after: `
package foo

func _() {
	foo := make(chan int)
	for ${1:} := range foo {
	$0
}
}
`,
		},
		{
			name: "var",
			before: `
package foo

func foo() (int, error) { return 0, nil }

func _() {
	foo().var
}
`,
			after: `
package foo

func foo() (int, error) { return 0, nil }

func _() {
	${1:}, ${2:} := foo()
}
`,
			allowMultipleItem: true,
		},
		{
			name: "var_single_value",
			before: `
package foo

func foo() error { return nil }

func _() {
	foo().var
}
`,
			allowMultipleItem: true,
			after: `
package foo

func foo() error { return nil }

func _() {
	${1:} := foo()
}
`,
		},
		{
			name: "var_same_type",
			before: `
package foo

func foo() (int, int) { return 0, 0 }

func _() {
	foo().var
}
`,
			after: `
package foo

func foo() (int, int) { return 0, 0 }

func _() {
	${1:}, ${2:} := foo()
}
`,
		},
		{
			name: "print_scalar",
			before: `
package foo

func _() {
	var foo int
	foo.print
}
`,
			after: `
package foo

import "fmt"

func _() {
	var foo int
	fmt.Printf("foo: %v\n", foo)
}
`,
		},
		{
			name: "print_multi",
			before: `
package foo

func foo() (int, error) { return 0, nil }

func _() {
	foo().print
}
`,
			after: `
package foo

import "fmt"

func foo() (int, error) { return 0, nil }

func _() {
	fmt.Println(foo())
}
`,
		},
		{
			name: "string split",
			before: `
package foo

func foo() []string {
	x := "test"
	return x.split
}`,
			after: `
package foo

import "strings"

func foo() []string {
	x := "test"
	return strings.Split(x, "$0")
}`,
		},
		{
			name: "string slice join",
			before: `
package foo

func foo() string {
	x := []string{"a", "test"}
	return x.join
}`,
			after: `
package foo

import "strings"

func foo() string {
	x := []string{"a", "test"}
	return strings.Join(x, "$0")
}`,
		},
		{
			name: "if not nil interface",
			before: `
package foo

func _() {
	var foo error
	foo.ifnotnil
}
`,
			after: `
package foo

func _() {
	var foo error
	if foo != nil {
	$0
}
}
`,
		},
		{
			name: "if not nil pointer",
			before: `
package foo

func _() {
	var foo *int
	foo.ifnotnil
}
`,
			after: `
package foo

func _() {
	var foo *int
	if foo != nil {
	$0
}
}
`,
		},
		{
			name: "if not nil slice",
			before: `
package foo

func _() {
	var foo []int
	foo.ifnotnil
}
`,
			after: `
package foo

func _() {
	var foo []int
	if foo != nil {
	$0
}
}
`,
		},
		{
			name: "if not nil map",
			before: `
package foo

func _() {
	var foo map[string]any
	foo.ifnotnil
}
`,
			after: `
package foo

func _() {
	var foo map[string]any
	if foo != nil {
	$0
}
}
`,
		},
		{
			name: "if not nil channel",
			before: `
package foo

func _() {
	var foo chan int
	foo.ifnotnil
}
`,
			after: `
package foo

func _() {
	var foo chan int
	if foo != nil {
	$0
}
}
`,
		},
		{
			name: "if not nil function",
			before: `
package foo

func _() {
	var foo func()
	foo.ifnotnil
}
`,
			after: `
package foo

func _() {
	var foo func()
	if foo != nil {
	$0
}
}
`,
		},
		{
			name: "slice_len",
			before: `
package foo

func _() {
	var foo []int
	foo.len
}
`,
			after: `
package foo

func _() {
	var foo []int
	len(foo)
}
`,
		},
		{
			name: "map_len",
			before: `
package foo

func _() {
	var foo map[string]int
	foo.len
}
`,
			after: `
package foo

func _() {
	var foo map[string]int
	len(foo)
}
`,
		},
		{
			name:              "slice_for",
			allowMultipleItem: true,
			before: `
package foo

func _() {
	var foo []int
	foo.for
}
`,
			after: `
package foo

func _() {
	var foo []int
	for ${1:} := range foo {
	$0
}
}
`,
		},
		{
			name:              "map_for",
			allowMultipleItem: true,
			before: `
package foo

func _() {
	var foo map[string]int
	foo.for
}
`,
			after: `
package foo

func _() {
	var foo map[string]int
	for ${1:} := range foo {
	$0
}
}
`,
		},
		{
			name:              "chan_for",
			allowMultipleItem: true,
			before: `
package foo

func _() {
	var foo chan int
	foo.for
}
`,
			after: `
package foo

func _() {
	var foo chan int
	for ${1:} := range foo {
	$0
}
}
`,
		},
		{
			name: "slice_forr",
			before: `
package foo

func _() {
	var foo []int
	foo.forr
}
`,
			after: `
package foo

func _() {
	var foo []int
	for ${1:}, ${2:} := range foo {
	$0
}
}
`,
		},
		{
			name: "slice_forr",
			before: `
package foo

func _() {
	var foo []int
	foo.forr
}
`,
			after: `
package foo

func _() {
	var foo []int
	for ${1:}, ${2:} := range foo {
	$0
}
}
`,
		},
		{
			name: "map_forr",
			before: `
package foo

func _() {
	var foo map[string]int
	foo.forr
}
`,
			after: `
package foo

func _() {
	var foo map[string]int
	for ${1:}, ${2:} := range foo {
	$0
}
}
`,
		},
	}

	r := WithOptions(
		Settings{
			"experimentalPostfixCompletions": true,
		},
	)
	r.Run(t, mod, func(t *testing.T, env *Env) {
		env.CreateBuffer("foo.go", "")

		for _, c := range cases {
			t.Run(c.name, func(t *testing.T) {
				c.before = strings.Trim(c.before, "\n")
				c.after = strings.Trim(c.after, "\n")

				env.SetBufferContent("foo.go", c.before)

				loc := env.RegexpSearch("foo.go", "\n}")
				completions := env.Completion(loc)
				if len(completions.Items) < 1 {
					t.Fatalf("expected at least one completion, got %v", completions.Items)
				}
				if !c.allowMultipleItem && len(completions.Items) > 1 {
					t.Fatalf("expected one completion, got %v", completions.Items)
				}

				env.AcceptCompletion(loc, completions.Items[0])

				if buf := env.BufferText("foo.go"); buf != c.after {
					t.Errorf("\nGOT:\n%s\nEXPECTED:\n%s", buf, c.after)
				}
			})
		}
	})
}
