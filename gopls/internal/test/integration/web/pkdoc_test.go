// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package web_test

import (
	"fmt"
	"html"
	"regexp"
	"strings"
	"testing"

	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/gopls/internal/settings"
	. "golang.org/x/tools/gopls/internal/test/integration"
)

// TODO(adonovan): define marker test verbs for checking package docs.

// TestBrowsePkgDoc provides basic coverage of the "Browse package
// documentation", which creates a web server on demand.
func TestBrowsePkgDoc(t *testing.T) {
	const files = `
-- go.mod --
module example.com

-- a/a.go --
package a

const A = 1

type G[T any] int
func (G[T]) F(int, int, int, int, int, int, int, ...int) {}

// EOF
`
	Run(t, files, func(t *testing.T, env *Env) {
		// Assert that the HTML page contains the expected const declaration.
		// (We may need to make allowances for HTML markup.)
		env.OpenFile("a/a.go")
		uri1 := viewPkgDoc(t, env, env.Sandbox.Workdir.EntireFile("a/a.go"))
		doc1 := get(t, uri1)
		checkMatch(t, true, doc1, "const A =.*1")

		// Regression test for signature truncation (#67287, #67294).
		checkMatch(t, true, doc1, regexp.QuoteMeta("func (G[T]) F(int, int, int, ...)"))

		// Check that edits to the buffer (even unsaved) are
		// reflected in the HTML document.
		env.RegexpReplace("a/a.go", "// EOF", "func NewFunc() {}")
		env.Await(env.DoneDiagnosingChanges())
		doc2 := get(t, uri1)
		checkMatch(t, true, doc2, "func NewFunc")

		// TODO(adonovan): assert some basic properties of the
		// HTML document using something like
		// golang.org/x/pkgsite/internal/testing/htmlcheck.

		// Grab the URL in the HTML source link for NewFunc.
		// (We don't have a DOM or JS interpreter so we have
		// to know something of the document internals here.)
		rx := regexp.MustCompile(`<h3 id='NewFunc'.*httpGET\("(.*)"\)`)
		srcURL := html.UnescapeString(string(rx.FindSubmatch(doc2)[1]))

		// Fetch the document. Its result isn't important,
		// but it must have the side effect of another showDocument
		// downcall, this time for a "file:" URL, causing the
		// client editor to navigate to the source file.
		t.Log("extracted /src URL", srcURL)
		collectDocs := env.Awaiter.ListenToShownDocuments()
		get(t, srcURL)

		// Check that shown location is that of NewFunc.
		shownSource := shownDocument(t, collectDocs(), "file:")
		gotLoc := protocol.Location{
			URI:   protocol.DocumentURI(shownSource.URI), // fishy conversion
			Range: *shownSource.Selection,
		}
		t.Log("showDocument(source file) URL:", gotLoc)
		wantLoc := env.RegexpSearch("a/a.go", `func ()NewFunc`)
		if gotLoc != wantLoc {
			t.Errorf("got location %v, want %v", gotLoc, wantLoc)
		}
	})
}

func TestShowDocumentUnsupported(t *testing.T) {
	const files = `
-- go.mod --
module example.com

-- a.go --
package a

const A = 1
`

	for _, supported := range []bool{false, true} {
		t.Run(fmt.Sprintf("supported=%v", supported), func(t *testing.T) {
			opts := []RunOption{Modes(Default)}
			if !supported {
				opts = append(opts, CapabilitiesJSON([]byte(`
{
	"window": {
		"showDocument": {
			"support": false
		}
	}
}`)))
			}
			WithOptions(opts...).Run(t, files, func(t *testing.T, env *Env) {
				env.OpenFile("a.go")
				// Invoke the "Browse package documentation" code
				// action to start the server.
				actions := env.CodeAction(env.Sandbox.Workdir.EntireFile("a.go"), nil, 0)
				docAction, err := codeActionByKind(actions, settings.GoDoc)
				if err != nil {
					t.Fatal(err)
				}

				// Execute the command.
				// Its side effect should be a single showDocument request.
				params := &protocol.ExecuteCommandParams{
					Command:   docAction.Command.Command,
					Arguments: docAction.Command.Arguments,
				}
				var result any
				collectDocs := env.Awaiter.ListenToShownDocuments()
				collectMessages := env.Awaiter.ListenToShownMessages()
				env.ExecuteCommand(params, &result)

				// golang/go#70342: just because the command has finished does not mean
				// that we will have received the necessary notifications. Synchronize
				// using progress reports.
				env.Await(CompletedWork(params.Command, 1, false))

				wantDocs, wantMessages := 0, 1
				if supported {
					wantDocs, wantMessages = 1, 0
				}

				docs := collectDocs()
				messages := collectMessages()

				if gotDocs := len(docs); gotDocs != wantDocs {
					t.Errorf("gopls.doc: got %d showDocument requests, want %d", gotDocs, wantDocs)
				}
				if gotMessages := len(messages); gotMessages != wantMessages {
					t.Errorf("gopls.doc: got %d showMessage requests, want %d", gotMessages, wantMessages)
				}
			})
		})
	}
}

func TestPkgDocNoPanic66449(t *testing.T) {
	// This particular input triggered a latent bug in doc.New
	// that would corrupt the AST while filtering out unexported
	// symbols such as b, causing nodeHTML to panic.
	// Now it doesn't crash.
	//
	// We also check cross-reference anchors for all symbols.
	const files = `
-- go.mod --
module example.com

-- a/a.go --
package a

// The 'π' suffix is to elimimate spurious matches with other HTML substrings,
// in particular the random base64 secret tokens that appear in gopls URLs.

var Vπ, vπ = 0, 0
const Cπ, cπ = 0, 0

func Fπ()
func fπ()

type Tπ int
type tπ int

func (Tπ) Mπ() {}
func (Tπ) mπ() {}

func (tπ) Mπ() {}
func (tπ) mπ() {}
`
	Run(t, files, func(t *testing.T, env *Env) {
		env.OpenFile("a/a.go")
		uri1 := viewPkgDoc(t, env, env.Sandbox.Workdir.EntireFile("a/a.go"))

		doc := get(t, uri1)
		// (Ideally our code rendering would also
		// eliminate unexported symbols...)
		checkMatch(t, true, doc, "var Vπ, vπ = .*0.*0")
		checkMatch(t, true, doc, "const Cπ, cπ = .*0.*0")

		// Unexported funcs/types/... must still be discarded.
		checkMatch(t, true, doc, "Fπ")
		checkMatch(t, false, doc, "fπ")
		checkMatch(t, true, doc, "Tπ")
		checkMatch(t, false, doc, "tπ")

		// Also, check that anchors exist (only) for exported symbols.
		// exported:
		checkMatch(t, true, doc, "<a id='Vπ'")
		checkMatch(t, true, doc, "<a id='Cπ'")
		checkMatch(t, true, doc, "<h3 id='Tπ'")
		checkMatch(t, true, doc, "<h3 id='Fπ'")
		checkMatch(t, true, doc, "<h4 id='Tπ.Mπ'")
		// unexported:
		checkMatch(t, false, doc, "<a id='vπ'")
		checkMatch(t, false, doc, "<a id='cπ'")
		checkMatch(t, false, doc, "<h3 id='tπ'")
		checkMatch(t, false, doc, "<h3 id='fπ'")
		checkMatch(t, false, doc, "<h4 id='Tπ.mπ'")
		checkMatch(t, false, doc, "<h4 id='tπ.Mπ'")
		checkMatch(t, false, doc, "<h4 id='tπ.mπ'")
	})
}

// TestPkgDocNavigation tests that the symbol selector and index of
// symbols are well formed.
func TestPkgDocNavigation(t *testing.T) {
	const files = `
-- go.mod --
module example.com

-- a/a.go --
package a

func Func1(int, string, bool, []string) (int, error)
func Func2(x, y int, a, b string) (int, error)

type Type struct {}
func (t Type) Method() {}
func (p *Type) PtrMethod() {}

func Constructor() Type
`
	Run(t, files, func(t *testing.T, env *Env) {
		env.OpenFile("a/a.go")
		uri1 := viewPkgDoc(t, env, env.Sandbox.Workdir.EntireFile("a/a.go"))
		doc := get(t, uri1)

		q := regexp.QuoteMeta

		// selector
		checkMatch(t, true, doc, q(`<option label='Func1(_, _, _, _)' value='#Func1'/>`))
		checkMatch(t, true, doc, q(`<option label='Func2(x, y, a, b)' value='#Func2'/>`))
		checkMatch(t, true, doc, q(`<option label='Type' value='#Type'/>`))
		checkMatch(t, true, doc, q(`<option label='Constructor()' value='#Constructor'/>`))
		checkMatch(t, true, doc, q(`<option label='(t) Method()' value='#Type.Method'/>`))
		checkMatch(t, true, doc, q(`<option label='(p) PtrMethod()' value='#Type.PtrMethod'/>`))

		// index
		checkMatch(t, true, doc, q(`<li><a href='#Func1'>func Func1(int, string, bool, ...) (int, error)</a></li>`))
		checkMatch(t, true, doc, q(`<li><a href='#Func2'>func Func2(x int, y int, a string, ...) (int, error)</a></li>`))
		checkMatch(t, true, doc, q(`<li><a href='#Type'>type Type</a></li>`))
		checkMatch(t, true, doc, q(`<li><a href='#Constructor'>func Constructor() Type</a></li>`))
		checkMatch(t, true, doc, q(`<li><a href='#Type.Method'>func (t Type) Method()</a></li>`))
		checkMatch(t, true, doc, q(`<li><a href='#Type.PtrMethod'>func (p *Type) PtrMethod()</a></li>`))
	})
}

// TestPkgDocContext tests that the gopls.doc command title and /pkg
// URL are appropriate for the current selection. It is effectively a
// test of golang.DocFragment.
func TestPkgDocContext(t *testing.T) {
	const files = `
-- go.mod --
module example.com

-- a/a.go --
package a

import "fmt"
import "bytes"

func A() {
	fmt.Println()
	new(bytes.Buffer).Write(nil)
}

const K = 123

type T int
func (*T) M() { /*in T.M*/}

`

	viewRE := regexp.MustCompile("view=[0-9]*")
	Run(t, files, func(t *testing.T, env *Env) {
		env.OpenFile("a/a.go")
		for _, test := range []struct {
			re   string // regexp indicating selected portion of input file
			want string // suffix of expected URL after /pkg/
		}{
			// current package
			{"package a", "example.com/a?view=1"},  // outside any decl
			{"in T.M", "example.com/a?view=1#T.M"}, // inside method (*T).M
			{"123", "example.com/a?view=1#K"},      // inside const/var decl
			{"T int", "example.com/a?view=1#T"},    // inside type decl

			// imported
			{"\"fmt\"", "fmt?view=1"},              // in import spec
			{"fmt[.]", "fmt?view=1"},               // use of PkgName
			{"Println", "fmt?view=1#Println"},      // use of imported pkg-level symbol
			{"fmt.Println", "fmt?view=1#Println"},  // qualified identifier
			{"Write", "bytes?view=1#Buffer.Write"}, // use of imported method

			// TODO(adonovan):
			// - xtest package -> ForTest
			// - field of imported struct -> nope
			// - exported method of nonexported type from another package
			//   (e.g. types.Named.Obj) -> nope
			// Also: assert that Command.Title looks nice.
		} {
			uri := viewPkgDoc(t, env, env.RegexpSearch("a/a.go", test.re))
			_, got, ok := strings.Cut(uri, "/pkg/")
			if !ok {
				t.Errorf("pattern %q => %s (invalid /pkg URL)", test.re, uri)
				continue
			}

			// Normalize the view ID, which varies by integration test mode.
			got = viewRE.ReplaceAllString(got, "view=1")

			if got != test.want {
				t.Errorf("pattern %q => %s; want %s", test.re, got, test.want)
			}
		}
	})
}

// TestPkgDocFileImports tests that the doc links are rendered
// as URLs based on the correct import mapping for the file in
// which they appear.
func TestPkgDocFileImports(t *testing.T) {
	const files = `
-- go.mod --
module mod.com
go 1.20

-- a/a1.go --
// Package a refers to [b.T] [b.U] [alias.D] [d.D] [c.T] [c.U] [nope.Nope]
package a

import "mod.com/b"
import alias "mod.com/d"

// [b.T] indeed refers to b.T.
//
// [alias.D] refers to d.D
// but [d.D] also refers to d.D.
type A1 int

-- a/a2.go --
package a

import b "mod.com/c"

// [b.U] actually refers to c.U.
type A2 int

-- b/b.go --
package b

type T int
type U int

-- c/c.go --
package c

type T int
type U int

-- d/d.go --
package d

type D int
`
	Run(t, files, func(t *testing.T, env *Env) {
		env.OpenFile("a/a1.go")
		uri1 := viewPkgDoc(t, env, env.Sandbox.Workdir.EntireFile("a/a1.go"))
		doc := get(t, uri1)

		// Check that the doc links are resolved using the
		// appropriate import mapping for the file in which
		// they appear.
		checkMatch(t, true, doc, `pkg/mod.com/b\?.*#T">b.T</a> indeed refers to b.T`)
		checkMatch(t, true, doc, `pkg/mod.com/c\?.*#U">b.U</a> actually refers to c.U`)

		// Check that doc links can be resolved using either
		// the original or the local name when they refer to a
		// renaming import. (Local names are preferred.)
		checkMatch(t, true, doc, `pkg/mod.com/d\?.*#D">alias.D</a> refers to d.D`)
		checkMatch(t, true, doc, `pkg/mod.com/d\?.*#D">d.D</a> also refers to d.D`)

		// Check that links in the package doc comment are
		// resolved, and relative to the correct file (a1.go).
		checkMatch(t, true, doc, `Package a refers to.*pkg/mod.com/b\?.*#T">b.T</a>`)
		checkMatch(t, true, doc, `Package a refers to.*pkg/mod.com/b\?.*#U">b.U</a>`)
		checkMatch(t, true, doc, `Package a refers to.*pkg/mod.com/d\?.*#D">alias.D</a>`)
		checkMatch(t, true, doc, `Package a refers to.*pkg/mod.com/d\?.*#D">d.D</a>`)
		checkMatch(t, true, doc, `Package a refers to.*pkg/mod.com/c\?.*#T">c.T</a>`)
		checkMatch(t, true, doc, `Package a refers to.*pkg/mod.com/c\?.*#U">c.U</a>`)
		checkMatch(t, true, doc, `Package a refers to.* \[nope.Nope\]`)
	})
}

// TestPkgDocConstructorOfUnexported tests that exported constructor
// functions (NewT) whose result type (t) is unexported are not
// discarded but are presented as ordinary top-level functions (#69553).
func TestPkgDocConstructorOfUnexported(t *testing.T) {
	const files = `
-- go.mod --
module mod.com
go 1.20

-- a/a.go --
package a

func A() {}
func Z() {}

type unexported int
func NewUnexported() unexported // exported constructor of unexported type

type Exported int
func NewExported() Exported // exported constructor of exported type
`
	Run(t, files, func(t *testing.T, env *Env) {
		env.OpenFile("a/a.go")
		uri1 := viewPkgDoc(t, env, env.Sandbox.Workdir.EntireFile("a/a.go"))
		doc := get(t, uri1)

		want := regexp.QuoteMeta(`
<optgroup label='Functions'>
  <option label='A()' value='#A'/>
  <option label='NewUnexported()' value='#NewUnexported'/>
  <option label='Z()' value='#Z'/>
</optgroup>
<optgroup label='Types'>
  <option label='Exported' value='#Exported'/>
</optgroup>
<optgroup label='type Exported'>
  <option label='NewExported()' value='#NewExported'/>
</optgroup>`)
		checkMatch(t, true, doc, want)
	})
}

// viewPkgDoc invokes the "Browse package documentation" code action
// at the specified location. It returns the URI of the document, or
// fails the test.
func viewPkgDoc(t *testing.T, env *Env, loc protocol.Location) protocol.URI {
	// Invoke the "Browse package documentation" code
	// action to start the server.
	actions := env.CodeAction(loc, nil, 0)
	docAction, err := codeActionByKind(actions, settings.GoDoc)
	if err != nil {
		t.Fatal(err)
	}

	// Execute the command.
	// Its side effect should be a single showDocument request.
	params := &protocol.ExecuteCommandParams{
		Command:   docAction.Command.Command,
		Arguments: docAction.Command.Arguments,
	}
	var result any
	collectDocs := env.Awaiter.ListenToShownDocuments()
	env.ExecuteCommand(params, &result)

	doc := shownDocument(t, collectDocs(), "http:")
	if doc == nil {
		t.Fatalf("no showDocument call had 'http:' prefix")
	}
	if false {
		t.Log("showDocument(package doc) URL:", doc.URI)
	}
	return doc.URI
}
