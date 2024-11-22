// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package completion

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"golang.org/x/telemetry/counter"
	"golang.org/x/telemetry/counter/countertest"
	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/gopls/internal/server"
	. "golang.org/x/tools/gopls/internal/test/integration"
	"golang.org/x/tools/gopls/internal/test/integration/fake"
	"golang.org/x/tools/gopls/internal/util/bug"
	"golang.org/x/tools/internal/testenv"
)

func TestMain(m *testing.M) {
	bug.PanicOnBugs = true
	os.Exit(Main(m))
}

const proxy = `
-- example.com@v1.2.3/go.mod --
module example.com

go 1.12
-- example.com@v1.2.3/blah/blah.go --
package blah

const Name = "Blah"
-- random.org@v1.2.3/go.mod --
module random.org

go 1.12
-- random.org@v1.2.3/blah/blah.go --
package hello

const Name = "Hello"
`

func TestPackageCompletion(t *testing.T) {
	const files = `
-- go.mod --
module mod.com

go 1.12
-- fruits/apple.go --
package apple

fun apple() int {
	return 0
}

-- fruits/testfile.go --
// this is a comment

/*
 this is a multiline comment
*/

import "fmt"

func test() {}

-- fruits/testfile2.go --
package

-- fruits/testfile3.go --
pac
-- 123f_r.u~its-123/testfile.go --
package

-- .invalid-dir@-name/testfile.go --
package
`
	var (
		testfile4 = ""
		testfile5 = "/*a comment*/ "
		testfile6 = "/*a comment*/\n"
	)
	for _, tc := range []struct {
		name          string
		filename      string
		content       *string
		triggerRegexp string
		want          []string
		editRegexp    string
	}{
		{
			name:          "package completion at valid position",
			filename:      "fruits/testfile.go",
			triggerRegexp: "\n()",
			want:          []string{"package apple", "package apple_test", "package fruits", "package fruits_test", "package main"},
			editRegexp:    "\n()",
		},
		{
			name:          "package completion in a comment",
			filename:      "fruits/testfile.go",
			triggerRegexp: "th(i)s",
			want:          nil,
		},
		{
			name:          "package completion in a multiline comment",
			filename:      "fruits/testfile.go",
			triggerRegexp: `\/\*\n()`,
			want:          nil,
		},
		{
			name:          "package completion at invalid position",
			filename:      "fruits/testfile.go",
			triggerRegexp: "import \"fmt\"\n()",
			want:          nil,
		},
		{
			name:          "package completion after package keyword",
			filename:      "fruits/testfile2.go",
			triggerRegexp: "package()",
			want:          []string{"package apple", "package apple_test", "package fruits", "package fruits_test", "package main"},
			editRegexp:    "package",
		},
		{
			name:          "package completion with 'pac' prefix",
			filename:      "fruits/testfile3.go",
			triggerRegexp: "pac()",
			want:          []string{"package apple", "package apple_test", "package fruits", "package fruits_test", "package main"},
			editRegexp:    "pac",
		},
		{
			name:          "package completion for empty file",
			filename:      "fruits/testfile4.go",
			triggerRegexp: "^$",
			content:       &testfile4,
			want:          []string{"package apple", "package apple_test", "package fruits", "package fruits_test", "package main"},
			editRegexp:    "^$",
		},
		{
			name:          "package completion without terminal newline",
			filename:      "fruits/testfile5.go",
			triggerRegexp: `\*\/ ()`,
			content:       &testfile5,
			want:          []string{"package apple", "package apple_test", "package fruits", "package fruits_test", "package main"},
			editRegexp:    `\*\/ ()`,
		},
		{
			name:          "package completion on terminal newline",
			filename:      "fruits/testfile6.go",
			triggerRegexp: `\*\/\n()`,
			content:       &testfile6,
			want:          []string{"package apple", "package apple_test", "package fruits", "package fruits_test", "package main"},
			editRegexp:    `\*\/\n()`,
		},
		// Issue golang/go#44680
		{
			name:          "package completion for dir name with punctuation",
			filename:      "123f_r.u~its-123/testfile.go",
			triggerRegexp: "package()",
			want:          []string{"package fruits123", "package fruits123_test", "package main"},
			editRegexp:    "package",
		},
		{
			name:          "package completion for invalid dir name",
			filename:      ".invalid-dir@-name/testfile.go",
			triggerRegexp: "package()",
			want:          []string{"package main"},
			editRegexp:    "package",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			Run(t, files, func(t *testing.T, env *Env) {
				if tc.content != nil {
					env.WriteWorkspaceFile(tc.filename, *tc.content)
					env.Await(env.DoneWithChangeWatchedFiles())
				}
				env.OpenFile(tc.filename)
				completions := env.Completion(env.RegexpSearch(tc.filename, tc.triggerRegexp))

				// Check that the completion item suggestions are in the range
				// of the file. {Start,End}.Line are zero-based.
				lineCount := len(strings.Split(env.BufferText(tc.filename), "\n"))
				for _, item := range completions.Items {
					for _, mode := range []string{"replace", "insert"} {
						edit, err := protocol.SelectCompletionTextEdit(item, mode == "replace")
						if err != nil {
							t.Fatalf("unexpected text edit in completion item (%v): %v", mode, err)
						}
						if start := int(edit.Range.Start.Line); start > lineCount {
							t.Fatalf("unexpected text edit range (%v) start line number: got %d, want <= %d", mode, start, lineCount)
						}
						if end := int(edit.Range.End.Line); end > lineCount {
							t.Fatalf("unexpected text edit range (%v) end line number: got %d, want <= %d", mode, end, lineCount)
						}
					}
				}

				if tc.want != nil {
					expectedLoc := env.RegexpSearch(tc.filename, tc.editRegexp)
					for _, item := range completions.Items {
						for _, mode := range []string{"replace", "insert"} {
							edit, _ := protocol.SelectCompletionTextEdit(item, mode == "replace")
							gotRng := edit.Range
							if expectedLoc.Range != gotRng {
								t.Errorf("unexpected completion range (%v) for completion item %s: got %v, want %v",
									mode, item.Label, gotRng, expectedLoc.Range)
							}
						}
					}
				}

				diff := compareCompletionLabels(tc.want, completions.Items)
				if diff != "" {
					t.Error(diff)
				}
			})
		})
	}
}

func TestPackageNameCompletion(t *testing.T) {
	const files = `
-- go.mod --
module mod.com

go 1.12
-- math/add.go --
package ma
`

	want := []string{"ma", "ma_test", "main", "math", "math_test"}
	Run(t, files, func(t *testing.T, env *Env) {
		env.OpenFile("math/add.go")
		completions := env.Completion(env.RegexpSearch("math/add.go", "package ma()"))

		diff := compareCompletionLabels(want, completions.Items)
		if diff != "" {
			t.Fatal(diff)
		}
	})
}

// TODO(rfindley): audit/clean up call sites for this helper, to ensure
// consistent test errors.
func compareCompletionLabels(want []string, gotItems []protocol.CompletionItem) string {
	var got []string
	for _, item := range gotItems {
		got = append(got, item.Label)
		if item.Label != item.InsertText && item.TextEdit == nil {
			// Label should be the same as InsertText, if InsertText is to be used
			return fmt.Sprintf("label not the same as InsertText %#v", item)
		}
	}

	if len(got) == 0 && len(want) == 0 {
		return "" // treat nil and the empty slice as equivalent
	}

	if diff := cmp.Diff(want, got); diff != "" {
		return fmt.Sprintf("completion item mismatch (-want +got):\n%s", diff)
	}
	return ""
}

func TestUnimportedCompletion(t *testing.T) {
	const mod = `
-- go.mod --
module mod.com

go 1.14

require example.com v1.2.3
-- go.sum --
example.com v1.2.3 h1:ihBTGWGjTU3V4ZJ9OmHITkU9WQ4lGdQkMjgyLFk0FaY=
example.com v1.2.3/go.mod h1:Y2Rc5rVWjWur0h3pd9aEvK5Pof8YKDANh9gHA2Maujo=
-- main.go --
package main

func main() {
	_ = blah
}
-- main2.go --
package main

import "example.com/blah"

func _() {
	_ = blah.Hello
}
`
	WithOptions(
		ProxyFiles(proxy),
	).Run(t, mod, func(t *testing.T, env *Env) {
		// Make sure the dependency is in the module cache and accessible for
		// unimported completions, and then remove it before proceeding.
		env.RemoveWorkspaceFile("main2.go")
		env.RunGoCommand("mod", "tidy")
		env.Await(env.DoneWithChangeWatchedFiles())

		// Trigger unimported completions for the example.com/blah package.
		env.OpenFile("main.go")
		env.Await(env.DoneWithOpen())
		loc := env.RegexpSearch("main.go", "ah")
		completions := env.Completion(loc)
		if len(completions.Items) == 0 {
			t.Fatalf("no completion items")
		}
		env.AcceptCompletion(loc, completions.Items[0]) // adds blah import to main.go
		env.Await(env.DoneWithChange())

		// Trigger completions once again for the blah.<> selector.
		env.RegexpReplace("main.go", "_ = blah", "_ = blah.")
		env.Await(env.DoneWithChange())
		loc = env.RegexpSearch("main.go", "\n}")
		completions = env.Completion(loc)
		if len(completions.Items) != 1 {
			t.Fatalf("expected 1 completion item, got %v", len(completions.Items))
		}
		item := completions.Items[0]
		if item.Label != "Name" {
			t.Fatalf("expected completion item blah.Name, got %v", item.Label)
		}
		env.AcceptCompletion(loc, item)

		// Await the diagnostics to add example.com/blah to the go.mod file.
		env.AfterChange(
			Diagnostics(env.AtRegexp("main.go", `"example.com/blah"`)),
		)
	})
}

// Test that completions still work with an undownloaded module, golang/go#43333.
func TestUndownloadedModule(t *testing.T) {
	// mod.com depends on example.com, but only in a file that's hidden by a
	// build tag, so the IWL won't download example.com. That will cause errors
	// in the go list -m call performed by the imports package.
	const files = `
-- go.mod --
module mod.com

go 1.14

require example.com v1.2.3
-- go.sum --
example.com v1.2.3 h1:ihBTGWGjTU3V4ZJ9OmHITkU9WQ4lGdQkMjgyLFk0FaY=
example.com v1.2.3/go.mod h1:Y2Rc5rVWjWur0h3pd9aEvK5Pof8YKDANh9gHA2Maujo=
-- useblah.go --
// +build hidden

package pkg
import "example.com/blah"
var _ = blah.Name
-- mainmod/mainmod.go --
package mainmod

const Name = "mainmod"
`
	WithOptions(ProxyFiles(proxy)).Run(t, files, func(t *testing.T, env *Env) {
		env.CreateBuffer("import.go", "package pkg\nvar _ = mainmod.Name\n")
		env.SaveBuffer("import.go")
		content := env.ReadWorkspaceFile("import.go")
		if !strings.Contains(content, `import "mod.com/mainmod`) {
			t.Errorf("expected import of mod.com/mainmod in %q", content)
		}
	})
}

// Test that we can doctor the source code enough so the file is
// parseable and completion works as expected.
func TestSourceFixup(t *testing.T) {
	const files = `
-- go.mod --
module mod.com

go 1.12
-- foo.go --
package foo

func _() {
	var s S
	if s.
}

type S struct {
	i int
}
`

	Run(t, files, func(t *testing.T, env *Env) {
		env.OpenFile("foo.go")
		completions := env.Completion(env.RegexpSearch("foo.go", `if s\.()`))
		diff := compareCompletionLabels([]string{"i"}, completions.Items)
		if diff != "" {
			t.Fatal(diff)
		}
	})
}

func TestCompletion_Issue45510(t *testing.T) {
	const files = `
-- go.mod --
module mod.com

go 1.12
-- main.go --
package main

func _() {
	type a *a
	var aaaa1, aaaa2 a
	var _ a = aaaa

	type b a
	var bbbb1, bbbb2 b
	var _ b = bbbb
}

type (
	c *d
	d *e
	e **c
)

func _() {
	var (
		xxxxc c
		xxxxd d
		xxxxe e
	)

	var _ c = xxxx
	var _ d = xxxx
	var _ e = xxxx
}
`

	Run(t, files, func(t *testing.T, env *Env) {
		env.OpenFile("main.go")

		tests := []struct {
			re   string
			want []string
		}{
			{`var _ a = aaaa()`, []string{"aaaa1", "aaaa2"}},
			{`var _ b = bbbb()`, []string{"bbbb1", "bbbb2"}},
			{`var _ c = xxxx()`, []string{"xxxxc", "xxxxd", "xxxxe"}},
			{`var _ d = xxxx()`, []string{"xxxxc", "xxxxd", "xxxxe"}},
			{`var _ e = xxxx()`, []string{"xxxxc", "xxxxd", "xxxxe"}},
		}
		for _, tt := range tests {
			completions := env.Completion(env.RegexpSearch("main.go", tt.re))
			diff := compareCompletionLabels(tt.want, completions.Items)
			if diff != "" {
				t.Errorf("%s: %s", tt.re, diff)
			}
		}
	})
}

func TestCompletionDeprecation(t *testing.T) {
	const files = `
-- go.mod --
module test.com

go 1.16
-- prog.go --
package waste
// Deprecated, use newFoof
func fooFunc() bool {
	return false
}

// Deprecated
const badPi = 3.14

func doit() {
	if fooF
	panic()
	x := badP
}
`
	Run(t, files, func(t *testing.T, env *Env) {
		env.OpenFile("prog.go")
		loc := env.RegexpSearch("prog.go", "if fooF")
		loc.Range.Start.Character += uint32(protocol.UTF16Len([]byte("if fooF")))
		completions := env.Completion(loc)
		diff := compareCompletionLabels([]string{"fooFunc"}, completions.Items)
		if diff != "" {
			t.Error(diff)
		}
		if completions.Items[0].Tags == nil {
			t.Errorf("expected Tags to show deprecation %#v", completions.Items[0].Tags)
		}
		loc = env.RegexpSearch("prog.go", "= badP")
		loc.Range.Start.Character += uint32(protocol.UTF16Len([]byte("= badP")))
		completions = env.Completion(loc)
		diff = compareCompletionLabels([]string{"badPi"}, completions.Items)
		if diff != "" {
			t.Error(diff)
		}
		if completions.Items[0].Tags == nil {
			t.Errorf("expected Tags to show deprecation %#v", completions.Items[0].Tags)
		}
	})
}

func TestUnimportedCompletion_VSCodeIssue1489(t *testing.T) {
	const src = `
-- go.mod --
module mod.com

go 1.14

-- main.go --
package main

import "fmt"

func main() {
	fmt.Println("a")
	math.Sqr
}
`
	WithOptions(
		WindowsLineEndings(),
		Settings{"ui.completion.usePlaceholders": true},
	).Run(t, src, func(t *testing.T, env *Env) {
		// Trigger unimported completions for the mod.com package.
		env.OpenFile("main.go")
		env.Await(env.DoneWithOpen())
		loc := env.RegexpSearch("main.go", "Sqr()")
		completions := env.Completion(loc)
		if len(completions.Items) == 0 {
			t.Fatalf("no completion items")
		}
		env.AcceptCompletion(loc, completions.Items[0])
		env.Await(env.DoneWithChange())
		got := env.BufferText("main.go")
		want := "package main\r\n\r\nimport (\r\n\t\"fmt\"\r\n\t\"math\"\r\n)\r\n\r\nfunc main() {\r\n\tfmt.Println(\"a\")\r\n\tmath.Sqrt(${1:x float64})\r\n}\r\n"
		if diff := cmp.Diff(want, got); diff != "" {
			t.Errorf("unimported completion (-want +got):\n%s", diff)
		}
	})
}

func TestUnimportedCompletion_VSCodeIssue3365(t *testing.T) {
	const src = `
-- go.mod --
module mod.com

go 1.19

-- main.go --
package main

func main() {
	println(strings.TLower)
}

var Lower = ""
`
	find := func(t *testing.T, completions *protocol.CompletionList, name string) protocol.CompletionItem {
		t.Helper()
		if completions == nil || len(completions.Items) == 0 {
			t.Fatalf("no completion items")
		}
		for _, i := range completions.Items {
			if i.Label == name {
				return i
			}
		}
		t.Fatalf("no item with label %q", name)
		return protocol.CompletionItem{}
	}

	for _, supportInsertReplace := range []bool{true, false} {
		t.Run(fmt.Sprintf("insertReplaceSupport=%v", supportInsertReplace), func(t *testing.T) {
			capabilities := fmt.Sprintf(`{ "textDocument": { "completion": { "completionItem": {"insertReplaceSupport":%t, "snippetSupport": false } } } }`, supportInsertReplace)
			runner := WithOptions(CapabilitiesJSON([]byte(capabilities)))
			runner.Run(t, src, func(t *testing.T, env *Env) {
				env.OpenFile("main.go")
				env.Await(env.DoneWithOpen())
				orig := env.BufferText("main.go")

				// We try to trigger completion at "println(strings.T<>Lower)"
				// and accept the completion candidate that matches the 'accept' label.
				insertModeWant := "println(strings.ToUpperLower)"
				if !supportInsertReplace {
					insertModeWant = "println(strings.ToUpper)"
				}
				testcases := []struct {
					mode   string
					accept string
					want   string
				}{
					{
						mode:   "insert",
						accept: "ToUpper",
						want:   insertModeWant,
					},
					{
						mode:   "insert",
						accept: "ToLower",
						want:   "println(strings.ToLower)", // The suffix 'Lower' is included in the text edit.
					},
					{
						mode:   "replace",
						accept: "ToUpper",
						want:   "println(strings.ToUpper)",
					},
					{
						mode:   "replace",
						accept: "ToLower",
						want:   "println(strings.ToLower)",
					},
				}

				for _, tc := range testcases {
					t.Run(fmt.Sprintf("%v/%v", tc.mode, tc.accept), func(t *testing.T) {

						env.SetSuggestionInsertReplaceMode(tc.mode == "replace")
						env.SetBufferContent("main.go", orig)
						loc := env.RegexpSearch("main.go", `Lower\)`)
						completions := env.Completion(loc)
						item := find(t, completions, tc.accept)
						env.AcceptCompletion(loc, item)
						env.Await(env.DoneWithChange())
						got := env.BufferText("main.go")
						if !strings.Contains(got, tc.want) {
							t.Errorf("unexpected state after completion:\n%v\nwanted %v", got, tc.want)
						}
					})
				}
			})
		})
	}
}
func TestUnimportedCompletionHasPlaceholders60269(t *testing.T) {
	// We can't express this as a marker test because it doesn't support AcceptCompletion.
	const src = `
-- go.mod --
module example.com
go 1.12

-- a/a.go --
package a

var _ = b.F

-- b/b.go --
package b

func F0(a, b int, c float64) {}
func F1(int, chan *string) {}
func F2[K, V any](map[K]V, chan V) {} // missing type parameters was issue #60959
func F3[K comparable, V any](map[K]V, chan V) {}
`
	WithOptions(
		WindowsLineEndings(),
		Settings{"ui.completion.usePlaceholders": true},
	).Run(t, src, func(t *testing.T, env *Env) {
		env.OpenFile("a/a.go")
		env.Await(env.DoneWithOpen())

		// The table lists the expected completions of b.F as they appear in Items.
		const common = "package a\r\n\r\nimport \"example.com/b\"\r\n\r\nvar _ = "
		for i, want := range []string{
			common + "b.F0(${1:a int}, ${2:b int}, ${3:c float64})\r\n",
			common + "b.F1(${1:_ int}, ${2:_ chan *string})\r\n",
			common + "b.F2[${1:K any}, ${2:V any}](${3:_ map[K]V}, ${4:_ chan V})\r\n",
			common + "b.F3[${1:K comparable}, ${2:V any}](${3:_ map[K]V}, ${4:_ chan V})\r\n",
		} {
			loc := env.RegexpSearch("a/a.go", "b.F()")
			completions := env.Completion(loc)
			if len(completions.Items) == 0 {
				t.Fatalf("no completion items")
			}
			saved := env.BufferText("a/a.go")
			env.AcceptCompletion(loc, completions.Items[i])
			env.Await(env.DoneWithChange())
			got := env.BufferText("a/a.go")
			if diff := cmp.Diff(want, got); diff != "" {
				t.Errorf("%d: unimported completion (-want +got):\n%s", i, diff)
			}
			env.SetBufferContent("a/a.go", saved) // restore
		}
	})
}

func TestPackageMemberCompletionAfterSyntaxError(t *testing.T) {
	// This test documents the current broken behavior due to golang/go#58833.
	const src = `
-- go.mod --
module mod.com

go 1.14

-- main.go --
package main

import "math"

func main() {
	math.Sqrt(,0)
	math.Ldex
}
`
	Run(t, src, func(t *testing.T, env *Env) {
		env.OpenFile("main.go")
		env.Await(env.DoneWithOpen())
		loc := env.RegexpSearch("main.go", "Ldex()")
		completions := env.Completion(loc)
		if len(completions.Items) == 0 {
			t.Fatalf("no completion items")
		}
		env.AcceptCompletion(loc, completions.Items[0])
		env.Await(env.DoneWithChange())
		got := env.BufferText("main.go")
		// The completion of math.Ldex after the syntax error on the
		// previous line is not "math.Ldexp" but "math.Ldexmath.Abs".
		// (In VSCode, "Abs" wrongly appears in the completion menu.)
		// This is a consequence of poor error recovery in the parser
		// causing "math.Ldex" to become a BadExpr.
		want := "package main\n\nimport \"math\"\n\nfunc main() {\n\tmath.Sqrt(,0)\n\tmath.Ldexmath.Abs(${1:})\n}\n"
		if diff := cmp.Diff(want, got); diff != "" {
			t.Errorf("unimported completion (-want +got):\n%s", diff)
		}
	})
}

func TestCompleteAllFields(t *testing.T) {
	// This test verifies that completion results always include all struct fields.
	// See golang/go#53992.

	const src = `
-- go.mod --
module mod.com

go 1.18

-- p/p.go --
package p

import (
	"fmt"

	. "net/http"
	. "runtime"
	. "go/types"
	. "go/parser"
	. "go/ast"
)

type S struct {
	a, b, c, d, e, f, g, h, i, j, k, l, m int
	n, o, p, q, r, s, t, u, v, w, x, y, z int
}

func _() {
	var s S
	fmt.Println(s.)
}
`

	WithOptions(Settings{
		"completionBudget": "1ns", // must be non-zero as 0 => infinity
	}).Run(t, src, func(t *testing.T, env *Env) {
		wantFields := make(map[string]bool)
		for c := 'a'; c <= 'z'; c++ {
			wantFields[string(c)] = true
		}

		env.OpenFile("p/p.go")
		// Make an arbitrary edit to ensure we're not hitting the cache.
		env.EditBuffer("p/p.go", fake.NewEdit(0, 0, 0, 0, fmt.Sprintf("// current time: %v\n", time.Now())))
		loc := env.RegexpSearch("p/p.go", `s\.()`)
		completions := env.Completion(loc)
		gotFields := make(map[string]bool)
		for _, item := range completions.Items {
			if item.Kind == protocol.FieldCompletion {
				gotFields[item.Label] = true
			}
		}

		if diff := cmp.Diff(wantFields, gotFields); diff != "" {
			t.Errorf("Completion(...) returned mismatching fields (-want +got):\n%s", diff)
		}
	})
}

func TestDefinition(t *testing.T) {
	files := `
-- go.mod --
module mod.com

go 1.18
-- a_test.go --
package foo
`
	tests := []struct {
		line string   // the sole line in the buffer after the package statement
		pat  string   // the pattern to search for
		want []string // expected completions
	}{
		{"func T", "T", []string{"TestXxx(t *testing.T)", "TestMain(m *testing.M)"}},
		{"func T()", "T", []string{"TestMain", "Test"}},
		{"func TestM", "TestM", []string{"TestMain(m *testing.M)", "TestM(t *testing.T)"}},
		{"func TestM()", "TestM", []string{"TestMain"}},
		{"func TestMi", "TestMi", []string{"TestMi(t *testing.T)"}},
		{"func TestMi()", "TestMi", nil},
		{"func TestG", "TestG", []string{"TestG(t *testing.T)"}},
		{"func TestG(", "TestG", nil},
		{"func Ben", "B", []string{"BenchmarkXxx(b *testing.B)"}},
		{"func Ben(", "Ben", []string{"Benchmark"}},
		{"func BenchmarkFoo", "BenchmarkFoo", []string{"BenchmarkFoo(b *testing.B)"}},
		{"func BenchmarkFoo(", "BenchmarkFoo", nil},
		{"func Fuz", "F", []string{"FuzzXxx(f *testing.F)"}},
		{"func Fuz(", "Fuz", []string{"Fuzz"}},
		{"func Testx", "Testx", nil},
		{"func TestMe(t *testing.T)", "TestMe", nil},
		{"func Te(t *testing.T)", "Te", []string{"TestMain", "Test"}},
	}
	fname := "a_test.go"
	Run(t, files, func(t *testing.T, env *Env) {
		env.OpenFile(fname)
		env.Await(env.DoneWithOpen())
		for _, test := range tests {
			env.SetBufferContent(fname, "package foo\n"+test.line)
			loc := env.RegexpSearch(fname, test.pat)
			loc.Range.Start.Character += uint32(protocol.UTF16Len([]byte(test.pat)))
			completions := env.Completion(loc)
			if diff := compareCompletionLabels(test.want, completions.Items); diff != "" {
				t.Error(diff)
			}
		}
	})
}

// Test that completing a definition replaces source text when applied, golang/go#56852.
func TestDefinitionReplaceRange(t *testing.T) {
	const mod = `
-- go.mod --
module mod.com

go 1.17
`

	tests := []struct {
		name          string
		before, after string
	}{
		{
			name: "func TestMa",
			before: `
package foo_test

func TestMa
`,
			after: `
package foo_test

func TestMain(m *testing.M)
`,
		},
		{
			name: "func TestSome",
			before: `
package foo_test

func TestSome
`,
			after: `
package foo_test

func TestSome(t *testing.T)
`,
		},
		{
			name: "func Bench",
			before: `
package foo_test

func Bench
`,
			// Note: Snippet with escaped }.
			after: `
package foo_test

func Benchmark${1:Xxx}(b *testing.B) {
	$0
\}
`,
		},
	}

	Run(t, mod, func(t *testing.T, env *Env) {
		env.CreateBuffer("foo_test.go", "")

		for _, tst := range tests {
			tst.before = strings.Trim(tst.before, "\n")
			tst.after = strings.Trim(tst.after, "\n")
			env.SetBufferContent("foo_test.go", tst.before)

			loc := env.RegexpSearch("foo_test.go", tst.name)
			loc.Range.Start.Character = uint32(protocol.UTF16Len([]byte(tst.name)))
			completions := env.Completion(loc)
			if len(completions.Items) == 0 {
				t.Fatalf("no completion items")
			}

			env.AcceptCompletion(loc, completions.Items[0])
			env.Await(env.DoneWithChange())
			if buf := env.BufferText("foo_test.go"); buf != tst.after {
				t.Errorf("%s:incorrect completion: got %q, want %q", tst.name, buf, tst.after)
			}
		}
	})
}

func TestGoWorkCompletion(t *testing.T) {
	const files = `
-- go.work --
go 1.18

use ./a
use ./a/ba
use ./a/b/
use ./dir/foo
use ./dir/foobar/
use ./missing/
-- a/go.mod --
-- go.mod --
-- a/bar/go.mod --
-- a/b/c/d/e/f/go.mod --
-- dir/bar --
-- dir/foobar/go.mod --
`

	Run(t, files, func(t *testing.T, env *Env) {
		env.OpenFile("go.work")

		tests := []struct {
			re   string
			want []string
		}{
			{`use ()\.`, []string{".", "./a", "./a/bar", "./dir/foobar"}},
			{`use \.()`, []string{"", "/a", "/a/bar", "/dir/foobar"}},
			{`use \./()`, []string{"a", "a/bar", "dir/foobar"}},
			{`use ./a()`, []string{"", "/b/c/d/e/f", "/bar"}},
			{`use ./a/b()`, []string{"/c/d/e/f", "ar"}},
			{`use ./a/b/()`, []string{`c/d/e/f`}},
			{`use ./a/ba()`, []string{"r"}},
			{`use ./dir/foo()`, []string{"bar"}},
			{`use ./dir/foobar/()`, []string{}},
			{`use ./missing/()`, []string{}},
		}
		for _, tt := range tests {
			completions := env.Completion(env.RegexpSearch("go.work", tt.re))
			diff := compareCompletionLabels(tt.want, completions.Items)
			if diff != "" {
				t.Errorf("%s: %s", tt.re, diff)
			}
		}
	})
}

const reverseInferenceSrcPrelude = `
-- go.mod --
module mod.com

go 1.18
-- a.go --
package a

type InterfaceA interface {
	implA()
}

type InterfaceB interface {
	implB()
}


type TypeA struct{}

func (TypeA) implA() {}

type TypeX string

func (TypeX) implB() {}

type TypeB struct{}

func (TypeB) implB() {}

type TypeC struct{} // should have no impact

type Wrap[T any] struct {
	inner *T
}

func NewWrap[T any](x T) Wrap[T] {
	return Wrap[T]{inner: &x}
}

func DoubleWrap[T any, U any](t T, u U) (Wrap[T], Wrap[U]) {
	return Wrap[T]{inner: &t}, Wrap[U]{inner: &u}
}

func IntWrap[T int32 | int64](x T) Wrap[T] {
	return Wrap[T]{inner: &x}
}

var ia InterfaceA
var ib InterfaceB

var avar TypeA
var bvar TypeB

var i int
var i32 int32
var i64 int64
`

func TestReverseInferCompletion(t *testing.T) {
	src := reverseInferenceSrcPrelude + `
	func main() {
		var _ Wrap[int64] = IntWrap()
	}
	`
	Run(t, src, func(t *testing.T, env *Env) {
		compl := env.RegexpSearch("a.go", `IntWrap\(()\)`)

		env.OpenFile("a.go")
		result := env.Completion(compl)

		wantLabel := []string{"i64", "i", "i32", "int64()"}

		// only check the prefix due to formatting differences with escaped characters
		wantText := []string{"i64", "int64(i", "int64(i32", "int64("}

		for i, item := range result.Items[:len(wantLabel)] {
			if diff := cmp.Diff(wantLabel[i], item.Label); diff != "" {
				t.Errorf("Completion: unexpected label mismatch (-want +got):\n%s", diff)
			}

			if insertText, ok := item.TextEdit.Value.(protocol.InsertReplaceEdit); ok {
				if diff := cmp.Diff(wantText[i], insertText.NewText[:len(wantText[i])]); diff != "" {
					t.Errorf("Completion: unexpected insertText mismatch (checks prefix only) (-want +got):\n%s", diff)
				}
			}
		}
	})
}

func TestInterfaceReverseInferCompletion(t *testing.T) {
	src := reverseInferenceSrcPrelude + `
	func main() {
		var wa Wrap[InterfaceA]
		var wb Wrap[InterfaceB]
		wb = NewWrap() // wb is of type Wrap[InterfaceB]
	}
	`

	Run(t, src, func(t *testing.T, env *Env) {
		compl := env.RegexpSearch("a.go", `NewWrap\(()\)`)

		env.OpenFile("a.go")
		result := env.Completion(compl)

		wantLabel := []string{"ib", "bvar", "wb.inner", "TypeB{}", "TypeX()", "nil"}

		// only check the prefix due to formatting differences with escaped characters
		wantText := []string{"ib", "InterfaceB(", "*wb.inner", "InterfaceB(", "InterfaceB(", "nil"}

		for i, item := range result.Items[:len(wantLabel)] {
			if diff := cmp.Diff(wantLabel[i], item.Label); diff != "" {
				t.Errorf("Completion: unexpected label mismatch (-want +got):\n%s", diff)
			}

			if insertText, ok := item.TextEdit.Value.(protocol.InsertReplaceEdit); ok {
				if diff := cmp.Diff(wantText[i], insertText.NewText[:len(wantText[i])]); diff != "" {
					t.Errorf("Completion: unexpected insertText mismatch (checks prefix only) (-want +got):\n%s", diff)
				}
			}
		}
	})
}

func TestInvalidReverseInferenceDefaultsToConstraintCompletion(t *testing.T) {
	src := reverseInferenceSrcPrelude + `
	func main() {
		var wa Wrap[InterfaceA]
		// This is ambiguous, so default to the constraint rather the inference.
		wa = IntWrap()
	}
	`
	Run(t, src, func(t *testing.T, env *Env) {
		compl := env.RegexpSearch("a.go", `IntWrap\(()\)`)

		env.OpenFile("a.go")
		result := env.Completion(compl)

		wantLabel := []string{"i32", "i64", "nil"}

		for i, item := range result.Items[:len(wantLabel)] {
			if diff := cmp.Diff(wantLabel[i], item.Label); diff != "" {
				t.Errorf("Completion: unexpected label mismatch (-want +got):\n%s", diff)
			}
		}
	})
}

func TestInterfaceReverseInferTypeParamCompletion(t *testing.T) {
	src := reverseInferenceSrcPrelude + `
	func main() {
		var wa Wrap[InterfaceA]
		var wb Wrap[InterfaceB]
		wb = NewWrap[]()
	}
	`

	Run(t, src, func(t *testing.T, env *Env) {
		compl := env.RegexpSearch("a.go", `NewWrap\[()\]\(\)`)

		env.OpenFile("a.go")
		result := env.Completion(compl)
		want := []string{"InterfaceB", "TypeB", "TypeX", "InterfaceA", "TypeA"}
		for i, item := range result.Items[:len(want)] {
			if diff := cmp.Diff(want[i], item.Label); diff != "" {
				t.Errorf("Completion: unexpected mismatch (-want +got):\n%s", diff)
			}
		}
	})
}

func TestInvalidReverseInferenceTypeParamDefaultsToConstraintCompletion(t *testing.T) {
	src := reverseInferenceSrcPrelude + `
	func main() {
		var wa Wrap[InterfaceA]
		// This is ambiguous, so default to the constraint rather the inference.
		wb = IntWrap[]()
	}
	`

	Run(t, src, func(t *testing.T, env *Env) {
		compl := env.RegexpSearch("a.go", `IntWrap\[()\]\(\)`)

		env.OpenFile("a.go")
		result := env.Completion(compl)
		want := []string{"int32", "int64"}
		for i, item := range result.Items[:len(want)] {
			if diff := cmp.Diff(want[i], item.Label); diff != "" {
				t.Errorf("Completion: unexpected mismatch (-want +got):\n%s", diff)
			}
		}
	})
}

func TestReverseInferDoubleTypeParamCompletion(t *testing.T) {
	src := reverseInferenceSrcPrelude + `
	func main() {
		var wa Wrap[InterfaceA]
		var wb Wrap[InterfaceB]

		wa, wb = DoubleWrap[]()
		// _ is necessary to trick the parser into an index list expression
		wa, wb = DoubleWrap[InterfaceA, _]()
	}
	`
	Run(t, src, func(t *testing.T, env *Env) {
		env.OpenFile("a.go")

		compl := env.RegexpSearch("a.go", `DoubleWrap\[()\]\(\)`)
		result := env.Completion(compl)

		wantLabel := []string{"InterfaceA", "TypeA", "InterfaceB", "TypeB", "TypeC"}

		for i, item := range result.Items[:len(wantLabel)] {
			if diff := cmp.Diff(wantLabel[i], item.Label); diff != "" {
				t.Errorf("Completion: unexpected label mismatch (-want +got):\n%s", diff)
			}
		}

		compl = env.RegexpSearch("a.go", `DoubleWrap\[InterfaceA, (_)\]\(\)`)
		result = env.Completion(compl)

		wantLabel = []string{"InterfaceB", "TypeB", "TypeX", "InterfaceA", "TypeA"}

		for i, item := range result.Items[:len(wantLabel)] {
			if diff := cmp.Diff(wantLabel[i], item.Label); diff != "" {
				t.Errorf("Completion: unexpected label mismatch (-want +got):\n%s", diff)
			}
		}
	})
}

func TestDoubleParamReturnCompletion(t *testing.T) {
	src := reverseInferenceSrcPrelude + `
	func concrete() (Wrap[InterfaceA], Wrap[InterfaceB]) {
		return DoubleWrap[]()
	}

	func concrete2() (Wrap[InterfaceA], Wrap[InterfaceB]) {
		return DoubleWrap[InterfaceA, _]()
	}
	`

	Run(t, src, func(t *testing.T, env *Env) {
		env.OpenFile("a.go")

		compl := env.RegexpSearch("a.go", `DoubleWrap\[()\]\(\)`)
		result := env.Completion(compl)

		wantLabel := []string{"InterfaceA", "TypeA", "InterfaceB", "TypeB", "TypeC"}

		for i, item := range result.Items[:len(wantLabel)] {
			if diff := cmp.Diff(wantLabel[i], item.Label); diff != "" {
				t.Errorf("Completion: unexpected label mismatch (-want +got):\n%s", diff)
			}
		}

		compl = env.RegexpSearch("a.go", `DoubleWrap\[InterfaceA, (_)\]\(\)`)
		result = env.Completion(compl)

		wantLabel = []string{"InterfaceB", "TypeB", "TypeX", "InterfaceA", "TypeA"}

		for i, item := range result.Items[:len(wantLabel)] {
			if diff := cmp.Diff(wantLabel[i], item.Label); diff != "" {
				t.Errorf("Completion: unexpected label mismatch (-want +got):\n%s", diff)
			}
		}
	})
}

func TestBuiltinCompletion(t *testing.T) {
	const files = `
-- go.mod --
module mod.com

go 1.18
-- a.go --
package a

func _() {
	// here
}
`

	Run(t, files, func(t *testing.T, env *Env) {
		env.OpenFile("a.go")
		result := env.Completion(env.RegexpSearch("a.go", `// here`))
		builtins := []string{
			"any", "append", "bool", "byte", "cap", "close",
			"comparable", "complex", "complex128", "complex64", "copy", "delete",
			"error", "false", "float32", "float64", "imag", "int", "int16", "int32",
			"int64", "int8", "len", "make", "new", "panic", "print", "println", "real",
			"recover", "rune", "string", "true", "uint", "uint16", "uint32", "uint64",
			"uint8", "uintptr", "nil",
		}
		if testenv.Go1Point() >= 21 {
			builtins = append(builtins, "clear", "max", "min")
		}
		sort.Strings(builtins)
		var got []string

		for _, item := range result.Items {
			// TODO(rfindley): for flexibility, ignore zero while it is being
			// implemented. Remove this if/when zero lands.
			if item.Label != "zero" {
				got = append(got, item.Label)
			}
		}
		sort.Strings(got)

		if diff := cmp.Diff(builtins, got); diff != "" {
			t.Errorf("Completion: unexpected mismatch (-want +got):\n%s", diff)
		}
	})
}

func TestOverlayCompletion(t *testing.T) {
	const files = `
-- go.mod --
module foo.test

go 1.18

-- foo/foo.go --
package foo

type Foo struct{}
`

	Run(t, files, func(t *testing.T, env *Env) {
		env.CreateBuffer("nodisk/nodisk.go", `
package nodisk

import (
	"foo.test/foo"
)

func _() {
	foo.Foo()
}
`)
		list := env.Completion(env.RegexpSearch("nodisk/nodisk.go", "foo.(Foo)"))
		want := []string{"Foo"}
		var got []string
		for _, item := range list.Items {
			got = append(got, item.Label)
		}
		if diff := cmp.Diff(want, got); diff != "" {
			t.Errorf("Completion: unexpected mismatch (-want +got):\n%s", diff)
		}
	})
}

// Fix for golang/go#60062: unimported completion included "golang.org/toolchain" results.
func TestToolchainCompletions(t *testing.T) {
	const files = `
-- go.mod --
module foo.test/foo

go 1.21

-- foo.go --
package foo

func _() {
	os.Open
}

func _() {
	strings
}
`

	const proxy = `
-- golang.org/toolchain@v0.0.1-go1.21.1.linux-amd64/go.mod --
module golang.org/toolchain
-- golang.org/toolchain@v0.0.1-go1.21.1.linux-amd64/src/os/os.go --
package os

func Open() {}
-- golang.org/toolchain@v0.0.1-go1.21.1.linux-amd64/src/strings/strings.go --
package strings

func Join() {}
`

	WithOptions(
		ProxyFiles(proxy),
	).Run(t, files, func(t *testing.T, env *Env) {
		env.RunGoCommand("mod", "download", "golang.org/toolchain@v0.0.1-go1.21.1.linux-amd64")
		env.OpenFile("foo.go")

		for _, pattern := range []string{"os.Open()", "string()"} {
			loc := env.RegexpSearch("foo.go", pattern)
			res := env.Completion(loc)
			for _, item := range res.Items {
				if strings.Contains(item.Detail, "golang.org/toolchain") {
					t.Errorf("Completion(...) returned toolchain item %#v", item)
				}
			}
		}
	})
}

// show that the efficacy counters get exercised. Fortuntely a small program
// exercises them all
func TestCounters(t *testing.T) {
	const files = `
-- go.mod --
module foo
go 1.21
-- x.go --
package foo

func main() {
}

`
	WithOptions(
		Modes(Default),
	).Run(t, files, func(t *testing.T, env *Env) {
		cts := func() map[*counter.Counter]uint64 {
			ans := make(map[*counter.Counter]uint64)
			for _, c := range server.CompletionCounters {
				ans[c], _ = countertest.ReadCounter(c)
			}
			return ans
		}
		before := cts()
		env.OpenFile("x.go")
		env.Await(env.DoneWithOpen())
		saved := env.BufferText("x.go")
		lines := strings.Split(saved, "\n")
		// make sure the unused counter is exercised
		loc := env.RegexpSearch("x.go", "main")
		loc.Range.End = loc.Range.Start
		env.Completion(loc)                       // ignore the proposed completions
		env.RegexpReplace("x.go", "main", "Main") // completions are unused
		env.SetBufferContent("x.go", saved)       // restore x.go
		// used:no

		// all the action is after 4 characters on line 2 (counting from 0)
		for i := 2; i < len(lines); i++ {
			l := lines[i]
			loc.Range.Start.Line = uint32(i)
			for j := 4; j < len(l); j++ {
				loc.Range.Start.Character = uint32(j)
				loc.Range.End = loc.Range.Start
				res := env.Completion(loc)
				if len(res.Items) > 0 {
					r := res.Items[0]
					env.AcceptCompletion(loc, r)
					env.SetBufferContent("x.go", saved)
				}
			}
		}
		after := cts()
		for c := range after {
			if after[c] <= before[c] {
				t.Errorf("%s did not increase", c.Name())
			}
		}
	})
}
