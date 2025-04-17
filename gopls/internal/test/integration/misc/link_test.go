// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package misc

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"golang.org/x/tools/gopls/internal/protocol"
	. "golang.org/x/tools/gopls/internal/test/integration"
)

func TestHoverAndDocumentLink(t *testing.T) {
	const program = `
-- go.mod --
module mod.test

go 1.12

require import.test v1.2.3

require replace.test v1.2.3
replace replace.test => replace.test v1.2.4

require replace.fixed.test v1.2.3
replace replace.fixed.test v1.2.3 => replace.fixed.test v1.2.4

require replace.another.test v1.2.3
replace replace.another.test => another.test v1.2.3


replace example.com/non-exist => ./
replace example.com/non-exist1 => ../work/

-- main.go --
package main

import "import.test/pkg"
import "replace.test/replace"
import "replace.fixed.test/fixed"
import "replace.another.test/another"

func main() {
	// Issue 43990: this is not a link that most users can open from an LSP
	// client: mongodb://not.a.link.com
	println(pkg.Hello)
	println(replace.Hello)
	println(fixed.Hello)
	println(another.Hello)
}`

	const proxy = `
-- import.test@v1.2.3/go.mod --
module import.test

go 1.12
-- import.test@v1.2.3/pkg/const.go --
package pkg


-- replace.test@v1.2.4/go.mod --
module replace.test

go 1.12
-- replace.test@v1.2.4/replace/const.go --
package replace

const Hello = "Hello"

-- replace.fixed.test@v1.2.4/go.mod --
module replace.fixed.test

go 1.12
-- replace.fixed.test@v1.2.4/fixed/const.go --
package fixed

const Hello = "Hello"

-- another.test@v1.2.3/go.mod --
module another.test

go 1.12
-- another.test@v1.2.3/another/const.go --
package another

const Hello = "Hello"
`
	WithOptions(
		ProxyFiles(proxy),
		WriteGoSum("."),
	).Run(t, program, func(t *testing.T, env *Env) {
		env.OpenFile("main.go")
		env.OpenFile("go.mod")

		const (
			modImportLink        = "https://pkg.go.dev/mod/import.test@v1.2.3"
			modReplaceLink       = "https://pkg.go.dev/mod/replace.test@v1.2.4"
			modReplaceFixedeLink = "https://pkg.go.dev/mod/replace.fixed.test@v1.2.4"
			modAnotherLink       = "https://pkg.go.dev/mod/another.test@v1.2.3"

			pkgImportLink       = "https://pkg.go.dev/import.test@v1.2.3/pkg"
			pkgReplaceLink      = "https://pkg.go.dev/replace.test@v1.2.4/replace"
			pkgReplaceFixedLink = "https://pkg.go.dev/replace.fixed.test@v1.2.4/fixed"
			pkgAnotherLink      = "https://pkg.go.dev/another.test@v1.2.3/another"
		)

		// First, check that we get the expected links via hover and documentLink.
		content, _ := env.Hover(env.RegexpSearch("main.go", "pkg.Hello"))
		if content == nil || !strings.Contains(content.Value, pkgImportLink) {
			t.Errorf("hover: got %v in main.go, want contains %q", content, pkgImportLink)
		}
		content, _ = env.Hover(env.RegexpSearch("main.go", "replace.Hello"))
		if content == nil || !strings.Contains(content.Value, pkgReplaceLink) {
			t.Errorf("hover: got %v in main.go, want contains %q", content, pkgReplaceLink)
		}
		content, _ = env.Hover(env.RegexpSearch("main.go", "fixed.Hello"))
		if content == nil || !strings.Contains(content.Value, pkgReplaceFixedLink) {
			t.Errorf("hover: got %v in main.go, want contains %q", content, pkgReplaceFixedLink)
		}
		content, _ = env.Hover(env.RegexpSearch("main.go", "another.Hello"))
		if content == nil || !strings.Contains(content.Value, pkgAnotherLink) {
			t.Errorf("hover: got %v in main.go, want contains %q", content, pkgAnotherLink)
		}

		content, _ = env.Hover(env.RegexpSearch("go.mod", "import.test"))
		if content == nil || !strings.Contains(content.Value, pkgImportLink) {
			t.Errorf("hover: got %v in main.go, want contains %q", content, pkgImportLink)
		}
		content, _ = env.Hover(env.RegexpSearch("go.mod", "replace.test"))
		if content == nil || !strings.Contains(content.Value, pkgReplaceLink) {
			t.Errorf("hover: got %v in main.go, want contains %q", content, pkgReplaceLink)
		}
		content, _ = env.Hover(env.RegexpSearch("go.mod", "replace.fixed.test"))
		if content == nil || !strings.Contains(content.Value, pkgReplaceFixedLink) {
			t.Errorf("hover: got %v in main.go, want contains %q", content, pkgReplaceFixedLink)
		}
		content, _ = env.Hover(env.RegexpSearch("go.mod", "replace.another.test"))
		if content == nil || !strings.Contains(content.Value, pkgAnotherLink) {
			t.Errorf("hover: got %v in main.go, want contains %q", content, pkgAnotherLink)
		}

		getLinks := func(links []protocol.DocumentLink) []string {
			var got []string
			for i := range links {
				got = append(got, *links[i].Target)
			}
			return got
		}
		links := env.DocumentLink("main.go")
		got, want := getLinks(links), []string{
			pkgImportLink,
			pkgReplaceLink,
			pkgReplaceFixedLink,
			pkgAnotherLink,
		}
		if !slices.Equal(got, want) {
			t.Errorf("documentLink: got links %v for main.go, want links %v", got, want)
		}

		links = env.DocumentLink("go.mod")
		localReplacePath := filepath.Join(env.Sandbox.Workdir.RootURI().Path(), "go.mod")
		got, want = getLinks(links), []string{
			localReplacePath, localReplacePath,
			modImportLink,
			modReplaceLink,
			modReplaceFixedeLink,
			modAnotherLink,
		}
		if !slices.Equal(got, want) {
			t.Errorf("documentLink: got links %v for go.mod, want links %v", got, want)
		}

		// Then change the environment to make these links private.
		cfg := env.Editor.Config()
		cfg.Env = map[string]string{"GOPRIVATE": "import.test"}
		env.ChangeConfiguration(cfg)

		// Finally, verify that the links are gone.
		content, _ = env.Hover(env.RegexpSearch("main.go", "pkg.Hello"))
		if content == nil || strings.Contains(content.Value, pkgImportLink) {
			t.Errorf("hover: got %v in main.go, want non-empty hover without %q", content, pkgImportLink)
		}
		content, _ = env.Hover(env.RegexpSearch("go.mod", "import.test"))
		if content == nil || strings.Contains(content.Value, modImportLink) {
			t.Errorf("hover: got %v in go.mod, want contains %q", content, modImportLink)
		}

		links = env.DocumentLink("main.go")
		got, want = getLinks(links), []string{
			pkgReplaceLink,
			pkgReplaceFixedLink,
			pkgAnotherLink,
		}
		if !slices.Equal(got, want) {
			t.Errorf("documentLink: got links %v for main.go, want links %v", got, want)
		}

		links = env.DocumentLink("go.mod")
		got, want = getLinks(links), []string{
			localReplacePath, localReplacePath,
			modReplaceLink,
			modReplaceFixedeLink,
			modAnotherLink,
		}
		if !slices.Equal(got, want) {
			t.Errorf("documentLink: got links %v for go.mod, want links %v", got, want)
		}
	})
}
