// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package web_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"golang.org/x/tools/gopls/internal/golang/splitpkg"
	"golang.org/x/tools/gopls/internal/settings"
	. "golang.org/x/tools/gopls/internal/test/integration"
)

// TestSplitPackage is a basic test of the web-based split package tool.
func TestSplitPackage(t *testing.T) {
	const files = `
-- go.mod --
module example.com

-- a/a.go --
package a

func a() { b1() }

func b1() { b2() }
func b2() { b1(); c() }

// EOF
-- a/b.go --
package a

func c() { d() }

func d() {}
`
	Run(t, files, func(t *testing.T, env *Env) {
		env.OpenFile("a/a.go")

		// Get the web page and do some rudimentary checks.
		// Most of the action happens in *.js (which we can't test)
		// and in interaction with the JSON endpoints (which we can).
		loc := env.RegexpSearch("a/a.go", "package")
		uri, page := codeActionWebPage(t, env, settings.GoSplitPackage, loc)

		checkMatch(t, true, page, `<h1>Split package example.com/a</h1>`)

		// Now we interact using JSON, basically a trivial Go
		// version of the splitpkg.js code.

		// jsonHTTP performs a JSON-over-HTTP request to the specified path.
		jsonHTTP := func(method, path string, in, out any) {
			// Replace the /splitpkg portion of the main page's URL,
			// keeping everything else.
			u, err := url.Parse(uri)
			if err != nil {
				t.Fatalf("parsing URL: %v", err)
			}
			u.Path = strings.ReplaceAll(u.Path, "/splitpkg", path)

			// HTTP
			inJSON, err := json.Marshal(in)
			if err != nil {
				t.Fatalf("encoding input: %v", err)
			}
			t.Logf("%s: in=%s", path, inJSON)
			req, err := http.NewRequest(method, u.String(), bytes.NewReader(inJSON))
			if err != nil {
				t.Fatalf("NewRequest: %v", err)
			}
			req.Header.Set("Content-Type", "application/json")
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("HTTP request: %v", err)
			}
			defer resp.Body.Close()
			outJSON, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatalf("reading output: %v", err)
			}
			t.Logf("%s: out=%s", path, outJSON)
			if out != nil {
				if err := json.Unmarshal(outJSON, out); err != nil {
					t.Fatalf("decoding output: %v", err)
				}
			}
		}

		// checkFileDecls queries the current package's decls grouped by file
		// and asserts that they match the description of the wanted state.
		checkFileDecls := func(want string) {
			var res splitpkg.ResultJSON
			jsonHTTP("GET", "/splitpkg-json", nil, &res)

			var lines []string
			for _, file := range res.Files {
				var buf strings.Builder
				fmt.Fprintf(&buf, "file %s:", file.Base)
				for _, decl := range file.Decls {
					for _, spec := range decl.Specs {
						fmt.Fprintf(&buf, " %s %s;", decl.Kind, spec.Name)
					}
				}
				lines = append(lines, buf.String())
			}
			slices.Sort(lines)
			got := strings.Join(lines, "\n")

			if diff := cmp.Diff(want, got); diff != "" {
				t.Errorf("unexpected file decls:\ngot:\n%s\nwant:\n%s\ndiff:\n%s", got, want, diff)
			}
		}

		// checkEdges queries the current decomposition and asserts
		// that it matches the description of the wanted state.
		checkEdges := func(want string) {
			var res splitpkg.ResultJSON
			jsonHTTP("GET", "/splitpkg-json", nil, &res)

			var lines []string
			for _, edge := range res.Edges {
				var buf strings.Builder
				fmt.Fprintf(&buf, "edge %s -> %s:", res.Components.Names[edge.From], res.Components.Names[edge.To])
				if edge.Cyclic {
					buf.WriteString(" ⚠")
				}
				for _, ref := range edge.Refs {
					fmt.Fprintf(&buf, " %s -> %s;", ref.From, ref.To)
				}
				lines = append(lines, buf.String())
			}
			slices.Sort(lines)
			got := strings.Join(lines, "\n")
			if diff := cmp.Diff(want, got); diff != "" {
				t.Errorf("unexpected component edges:\ngot:\n%s\nwant:\n%s\ndiff:\n%s", got, want, diff)
			}
		}

		// Check the initial file/decl state.
		checkFileDecls(`
file a.go: func a; func b1; func b2;
file b.go: func c; func d;`[1:])

		// Check that the set of decls updates as we edit the files.
		env.RegexpReplace("a/a.go", "// EOF", "func b3() {}")
		env.Await(env.DoneDiagnosingChanges())
		checkFileDecls(`
file a.go: func a; func b1; func b2; func b3;
file b.go: func c; func d;`[1:])

		// Post a cyclic decomposition. Check the report.
		jsonHTTP("POST", "/splitpkg-components", splitpkg.ComponentsJSON{
			Names:       []string{"zero", "one", "two", "three"},
			Assignments: map[string]int{"a": 0, "b1": 1, "b2": 2, "c": 3, "d": 3},
		}, nil)
		checkEdges(`
edge one -> two: ⚠ b1 -> b2;
edge two -> one: ⚠ b2 -> b1;
edge two -> three: b2 -> c;
edge zero -> one: a -> b1;`[1:])

		// Post an acyclic decomposition. Check the report.
		jsonHTTP("POST", "/splitpkg-components", splitpkg.ComponentsJSON{
			Names:       []string{"zero", "one", "two", "three"},
			Assignments: map[string]int{"a": 0, "b1": 1, "b2": 1, "c": 2, "d": 3},
		}, nil)
		checkEdges(`
edge one -> two: b2 -> c;
edge two -> three: c -> d;
edge zero -> one: a -> b1;`[1:])
	})
}
