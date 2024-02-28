// Copyright 2022 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
package misc

// todo: rename file to extract_to_new_file_test.go after code review

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	. "golang.org/x/tools/gopls/internal/test/integration"

	"golang.org/x/tools/gopls/internal/protocol"
)

func dedent(s string) string {
	s = strings.TrimPrefix(s, "\n")
	indents := regexp.MustCompile("^\t*").FindString(s)
	return regexp.MustCompile(fmt.Sprintf("(?m)^\t{0,%d}", len(indents))).ReplaceAllString(s, "")
}

func indent(s string) string {
	return regexp.MustCompile("(?m)^").ReplaceAllString(s, "\t")
}

// compileTemplate replaces two █ characters in text and write to dest and returns
// the location enclosed by the two █
func compileTemplate(env *Env, text string, dest string) protocol.Location {
	i := strings.Index(text, "█")
	j := strings.LastIndex(text, "█")
	if strings.Count(text, "█") != 2 {
		panic("expecting exactly two █ characters in source")
	}
	out := text[:i] + text[i+len("█"):j] + text[j+len("█"):]
	env.Sandbox.Workdir.WriteFile(env.Ctx, dest, out)
	env.OpenFile(dest)
	loc, err := env.Editor.OffsetLocation(dest, i, j-len("█"))
	if err != nil {
		panic(err)
	}
	return loc
}

func TestExtractToNewFile(t *testing.T) {
	const files = `
-- go.mod --
module mod.com

go 1.18
-- main.go --
package main

-- existing.go --
package main

-- existing2.go --
package main

-- existing2.1.go --
package main

`
	for _, tc := range []struct {
		name            string
		source          string
		fixed           string
		createdFilename string
		created         string
	}{
		{
			name: "func declaration",
			source: `
				package main
				
				func _() {}

				// fn docs
				█func fn() {}█
			`,
			fixed: `
				package main

				func _() {}
				
			`,
			createdFilename: "fn.go",
			created: `
				package main

				// fn docs
				func fn() {}
			`,
		},
		{
			name: "only select function name",
			source: `
				package main
				func █F█() {}
			`,
			fixed: `
				package main
			`,
			createdFilename: "f.go",
			created: `
				package main

				func F() {}
			`,
		},
		{
			name: "zero-width range",
			source: `
				package main
				func ██F() {}
			`,
			fixed: `
				package main
			`,
			createdFilename: "f.go",
			created: `
				package main

				func F() {}
			`,
		},
		{
			name: "type declaration",
			source: `
				package main

				// T docs
				█type T int
				type S int█
			`,
			fixed: `
				package main

			`,
			createdFilename: "t.go",
			created: `
				package main
				
				// T docs
				type T int
				type S int
			`,
		},
		{
			name: "const and var declaration",
			source: `
				package main

				// c docs
				█const c = 0
				var v = 0█
			`,
			fixed: `
				package main

			`,
			createdFilename: "c.go",
			created: `
				package main
				
				// c docs
				const c = 0

				var v = 0
			`,
		},
		{
			name: "select only const keyword",
			source: `
				package main

				█const█ (
					A = iota
					B
					C
				)
			`,
			fixed: `
				package main

			`,
			createdFilename: "a.go",
			created: `
				package main
				
				const (
					A = iota
					B
					C
				)
			`,
		},
		{
			name: "select surrounding comments",
			source: `
				package main

				█// above

				func fn() {}
				
				// below█
			`,
			fixed: `
				package main

			`,
			createdFilename: "fn.go",
			created: `
				package main
				
				// above

				func fn() {}

				// below
			`,
		},

		{
			name: "create file name conflict",
			source: `
				package main
				█func existing() {}█
			`,
			fixed: `
				package main
			`,
			createdFilename: "existing.1.go",
			created: `
				package main

				func existing() {}
			`,
		},
		{
			name: "create file name conflict again",
			source: `
				package main
				█func existing2() {}█
			`,
			fixed: `
				package main
			`,
			createdFilename: "existing2.2.go",
			created: `
				package main

				func existing2() {}
			`,
		},
		{
			name: "imports",
			source: `
				package main
				import "fmt"
				█func F() {
					fmt.Println()
				}█
			`,
			fixed: `
				package main

			`,
			createdFilename: "f.go",
			created: `
				package main

				import (
					"fmt"
				)

				func F() {
					fmt.Println()
				}
			`,
		},
		{
			name: "import alias",
			source: `
				package main
				import fmt2 "fmt"
				█func F() {
					fmt2.Println()
				}█
			`,
			fixed: `
				package main

			`,
			createdFilename: "f.go",
			created: `
				package main

				import (
					fmt2 "fmt"
				)

				func F() {
					fmt2.Println()
				}
			`,
		},
		{
			name: "multiple imports",
			source: `
				package main
				import (
					"fmt"
					"log"
				)
				func init(){
					log.Println()
				}
				█func F() {
					fmt.Println()
				}█

			`,
			fixed: `
				package main
				import (
					
					"log"
				)
				func init(){
					log.Println()
				}
			`,
			createdFilename: "f.go",
			created: `
				package main

				import (
					"fmt"
				)

				func F() {
					fmt.Println()
				}
			`,
		},
		{
			name: "blank import",
			source: `
				package main
				import _ "fmt"
				█func F() {}█
			`,
			fixed: `
				package main
				import _ "fmt"
			`,
			createdFilename: "f.go",
			created: `
				package main

				func F() {}
			`,
		},
		{
			// This case is poorly handled
			name: "dot import",
			source: `
				package main
				import . "fmt"
				█func F() { Println() }█
			`,
			fixed: `
				package main
				import . "fmt"
			`,
			createdFilename: "f.go",
			created: `
				package main

				func F() { Println() }
			`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			Run(t, files, func(t *testing.T, env *Env) {
				tc.source, tc.fixed, tc.created = dedent(tc.source), dedent(tc.fixed), dedent(tc.created)
				filename := "source.go"
				loc := compileTemplate(env, tc.source, filename)
				actions, err := env.Editor.CodeAction(env.Ctx, loc, nil)
				if err != nil {
					t.Fatal(err)
				}
				var codeAction *protocol.CodeAction
				for _, action := range actions {
					if action.Title == "Extract declarations to new file" {
						codeAction = &action
						break
					}
				}
				if codeAction == nil {
					t.Fatal("cannot find Extract declarations to new file action")
				}

				env.ApplyCodeAction(*codeAction)
				got := env.BufferText(filename)
				if tc.fixed != got {
					t.Errorf(`incorrect output of fixed file:
source:
%s
got:
%s
want:
%s
`, indent(tc.source), indent(got), indent(tc.fixed))
				}
				gotMoved := env.BufferText(tc.createdFilename)
				if tc.created != gotMoved {
					t.Errorf(`incorrect output of created file:
source:
%s
got created file:
%s
want created file:
%s
`, indent(tc.source), indent(gotMoved), indent(tc.created))
				}

			})
		})
	}
}

func TestExtractToNewFileInvalidSelection(t *testing.T) {
	const files = `
-- go.mod --
module mod.com

go 1.18
-- main.go --
package main

`
	for _, tc := range []struct {
		name   string
		source string
	}{
		{
			name: "select package declaration",
			source: `
				█package main█
				func fn() {}
			`,
		},
		{
			name: "select imports",
			source: `
				package main
				█import fmt█
			`,
		},
		{
			name: "select only comment",
			source: `
				package main
				█// comment█
			`,
		},
		{
			name: "selection does not contain whole top-level node",
			source: `
				package main
				func fn() {
					█print(0)█
				}
			`,
		},
		{
			name: "selection cross a comment",
			source: `
				package main

				█func fn() {} // comment█ comment 
			`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			Run(t, files, func(t *testing.T, env *Env) {
				filename := "source.go"
				loc := compileTemplate(env, dedent(tc.source), filename)
				actions, err := env.Editor.CodeAction(env.Ctx, loc, nil)
				if err != nil {
					t.Fatal(err)
				}

				for _, action := range actions {
					if action.Title == "Extract declarations to new file" {
						t.Errorf("should not offer code action")
						return
					}
				}
			})
		})
	}
}
