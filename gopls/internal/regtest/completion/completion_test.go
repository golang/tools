// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package completion

import (
	"fmt"
	"strings"
	"testing"

	. "golang.org/x/tools/gopls/internal/regtest"

	"golang.org/x/tools/internal/lsp/fake"
	"golang.org/x/tools/internal/lsp/protocol"
	"golang.org/x/tools/internal/testenv"
)

func TestMain(m *testing.M) {
	Main(m)
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
	testenv.NeedsGo1Point(t, 14)
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
			name:          "package completion after keyword 'package'",
			filename:      "fruits/testfile2.go",
			triggerRegexp: "package()",
			want:          []string{"package apple", "package apple_test", "package fruits", "package fruits_test", "package main"},
			editRegexp:    "package\n",
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
	} {
		t.Run(tc.name, func(t *testing.T) {
			Run(t, files, func(t *testing.T, env *Env) {
				if tc.content != nil {
					env.WriteWorkspaceFile(tc.filename, *tc.content)
					env.Await(
						env.DoneWithChangeWatchedFiles(),
					)
				}
				env.OpenFile(tc.filename)
				completions := env.Completion(tc.filename, env.RegexpSearch(tc.filename, tc.triggerRegexp))

				// Check that the completion item suggestions are in the range
				// of the file.
				lineCount := len(strings.Split(env.Editor.BufferText(tc.filename), "\n"))
				for _, item := range completions.Items {
					if start := int(item.TextEdit.Range.Start.Line); start >= lineCount {
						t.Fatalf("unexpected text edit range start line number: got %d, want less than %d", start, lineCount)
					}
					if end := int(item.TextEdit.Range.End.Line); end >= lineCount {
						t.Fatalf("unexpected text edit range end line number: got %d, want less than %d", end, lineCount)
					}
				}

				if tc.want != nil {
					start, end := env.RegexpRange(tc.filename, tc.editRegexp)
					expectedRng := protocol.Range{
						Start: fake.Pos.ToProtocolPosition(start),
						End:   fake.Pos.ToProtocolPosition(end),
					}
					for _, item := range completions.Items {
						gotRng := item.TextEdit.Range
						if expectedRng != gotRng {
							t.Errorf("unexpected completion range for completion item %s: got %v, want %v",
								item.Label, gotRng, expectedRng)
						}
					}
				}

				diff := compareCompletionResults(tc.want, completions.Items)
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
		completions := env.Completion("math/add.go", fake.Pos{
			Line:   0,
			Column: 10,
		})

		diff := compareCompletionResults(want, completions.Items)
		if diff != "" {
			t.Fatal(diff)
		}
	})
}

func compareCompletionResults(want []string, gotItems []protocol.CompletionItem) string {
	if len(gotItems) != len(want) {
		return fmt.Sprintf("got %v completion(s), want %v", len(gotItems), len(want))
	}

	var got []string
	for _, item := range gotItems {
		got = append(got, item.Label)
	}

	for i, v := range got {
		if v != want[i] {
			return fmt.Sprintf("completion results are not the same: got %v, want %v", got, want)
		}
	}

	return ""
}

func TestUnimportedCompletion(t *testing.T) {
	testenv.NeedsGo1Point(t, 14)

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
		pos := env.RegexpSearch("main.go", "ah")
		completions := env.Completion("main.go", pos)
		if len(completions.Items) == 0 {
			t.Fatalf("no completion items")
		}
		env.AcceptCompletion("main.go", pos, completions.Items[0])
		env.Await(env.DoneWithChange())

		// Trigger completions once again for the blah.<> selector.
		env.RegexpReplace("main.go", "_ = blah", "_ = blah.")
		env.Await(env.DoneWithChange())
		pos = env.RegexpSearch("main.go", "\n}")
		completions = env.Completion("main.go", pos)
		if len(completions.Items) != 1 {
			t.Fatalf("expected 1 completion item, got %v", len(completions.Items))
		}
		item := completions.Items[0]
		if item.Label != "Name" {
			t.Fatalf("expected completion item blah.Name, got %v", item.Label)
		}
		env.AcceptCompletion("main.go", pos, item)

		// Await the diagnostics to add example.com/blah to the go.mod file.
		env.SaveBufferWithoutActions("main.go")
		env.Await(
			env.DiagnosticAtRegexp("go.mod", "module mod.com"),
			env.DiagnosticAtRegexp("main.go", `"example.com/blah"`),
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
