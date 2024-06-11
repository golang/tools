// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package misc

import (
	"html"
	"io"
	"net/http"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/gopls/internal/protocol/command"
	. "golang.org/x/tools/gopls/internal/test/integration"
	"golang.org/x/tools/internal/testenv"
)

// TestWebServer exercises the web server created on demand
// for code actions such as "View package documentation".
func TestWebServer(t *testing.T) {
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
		uri1 := viewPkgDoc(t, env, "a/a.go")
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
		get(t, srcURL)

		// Check that that shown location is that of NewFunc.
		shownSource := shownDocument(t, env, "file:")
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

func TestRenderNoPanic66449(t *testing.T) {
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
		uri1 := viewPkgDoc(t, env, "a/a.go")
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

// TestRenderNavigation tests that the symbol selector and index of
// symbols are well formed.
func TestRenderNavigation(t *testing.T) {
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
		uri1 := viewPkgDoc(t, env, "a/a.go")
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

// viewPkgDoc invokes the "View package documentation" code action in
// the specified file. It returns the URI of the document, or fails
// the test.
func viewPkgDoc(t *testing.T, env *Env, filename string) protocol.URI {
	env.OpenFile(filename)

	// Invoke the "View package documentation" code
	// action to start the server.
	var docAction *protocol.CodeAction
	actions := env.CodeActionForFile(filename, nil)
	for _, act := range actions {
		if act.Title == "View package documentation" {
			docAction = &act
			break
		}
	}
	if docAction == nil {
		t.Fatalf("can't find action with Title 'View package documentation', only %#v",
			actions)
	}

	// Execute the command.
	// Its side effect should be a single showDocument request.
	params := &protocol.ExecuteCommandParams{
		Command:   docAction.Command.Command,
		Arguments: docAction.Command.Arguments,
	}
	var result command.DebuggingResult
	env.ExecuteCommand(params, &result)

	doc := shownDocument(t, env, "http:")
	if doc == nil {
		t.Fatalf("no showDocument call had 'http:' prefix")
	}
	t.Log("showDocument(package doc) URL:", doc.URI)
	return doc.URI
}

// TestFreeSymbols is a basic test of interaction with the "free symbols" web report.
func TestFreeSymbols(t *testing.T) {
	const files = `
-- go.mod --
module example.com

-- a/a.go --
package a

import "fmt"
import "bytes"

func f(buf bytes.Buffer, greeting string) {
/* « */
	fmt.Fprintf(&buf, "%s", greeting)
	buf.WriteString(fmt.Sprint("foo"))
	buf.WriteByte(0)
/* » */
	buf.Write(nil)
}
`
	Run(t, files, func(t *testing.T, env *Env) {
		env.OpenFile("a/a.go")

		// Invoke the "View free symbols" code
		// action to start the server.
		loc := env.RegexpSearch("a/a.go", "«((?:.|\n)*)»")
		actions, err := env.Editor.CodeAction(env.Ctx, loc, nil, protocol.CodeActionUnknownTrigger)
		if err != nil {
			t.Fatalf("CodeAction: %v", err)
		}
		var action *protocol.CodeAction
		for _, a := range actions {
			if a.Title == "View free symbols" {
				action = &a
				break
			}
		}
		if action == nil {
			t.Fatalf("can't find action with Title 'View free symbols', only %#v",
				actions)
		}

		// Execute the command.
		// Its side effect should be a single showDocument request.
		params := &protocol.ExecuteCommandParams{
			Command:   action.Command.Command,
			Arguments: action.Command.Arguments,
		}
		var result command.DebuggingResult
		env.ExecuteCommand(params, &result)
		doc := shownDocument(t, env, "http:")
		if doc == nil {
			t.Fatalf("no showDocument call had 'file:' prefix")
		}
		t.Log("showDocument(package doc) URL:", doc.URI)

		// Get the report and do some minimal checks for sensible results.
		report := get(t, doc.URI)
		checkMatch(t, true, report, `<li>import "<a .*'>fmt</a>" // for Fprintf, Sprint</li>`)
		checkMatch(t, true, report, `<li>var <a .*>buf</a>  bytes.Buffer</li>`)
		checkMatch(t, true, report, `<li>func <a .*>WriteByte</a>  func\(c byte\) error</li>`)
		checkMatch(t, true, report, `<li>func <a .*>WriteString</a>  func\(s string\) \(n int, err error\)</li>`)
		checkMatch(t, false, report, `<li>func <a .*>Write</a>`) // not in selection
		checkMatch(t, true, report, `<li>var <a .*>greeting</a>  string</li>`)
	})
}

// TestAssembly is a basic test of the web-based assembly listing.
func TestAssembly(t *testing.T) {
	testenv.NeedsGo1Point(t, 22) // for up-to-date assembly listing

	const files = `
-- go.mod --
module example.com

-- a/a.go --
package a

func f() {
	println("hello")
	defer println("world")
}

func g() {
	println("goodbye")
}
`
	Run(t, files, func(t *testing.T, env *Env) {
		env.OpenFile("a/a.go")

		// Invoke the "View assembly" code action to start the server.
		loc := env.RegexpSearch("a/a.go", "println")
		actions, err := env.Editor.CodeAction(env.Ctx, loc, nil, protocol.CodeActionUnknownTrigger)
		if err != nil {
			t.Fatalf("CodeAction: %v", err)
		}
		const wantTitle = "View " + runtime.GOARCH + " assembly for f"
		var action *protocol.CodeAction
		for _, a := range actions {
			if a.Title == wantTitle {
				action = &a
				break
			}
		}
		if action == nil {
			t.Fatalf("can't find action with Title %s, only %#v",
				wantTitle, actions)
		}

		// Execute the command.
		// Its side effect should be a single showDocument request.
		params := &protocol.ExecuteCommandParams{
			Command:   action.Command.Command,
			Arguments: action.Command.Arguments,
		}
		var result command.DebuggingResult
		env.ExecuteCommand(params, &result)
		doc := shownDocument(t, env, "http:")
		if doc == nil {
			t.Fatalf("no showDocument call had 'file:' prefix")
		}
		t.Log("showDocument(package doc) URL:", doc.URI)

		// Get the report and do some minimal checks for sensible results.
		// Use only portable instructions below!
		report := get(t, doc.URI)
		checkMatch(t, true, report, `TEXT.*example.com/a.f`)
		checkMatch(t, true, report, `CALL	runtime.printlock`)
		checkMatch(t, true, report, `CALL	runtime.printstring`)
		checkMatch(t, true, report, `CALL	runtime.printunlock`)
		checkMatch(t, true, report, `CALL	example.com/a.f.deferwrap1`)
		checkMatch(t, true, report, `RET`)
		checkMatch(t, true, report, `CALL	runtime.morestack_noctxt`)

		// Nested functions are also shown.
		checkMatch(t, true, report, `TEXT.*example.com/a.f.deferwrap1`)

		// But other functions are not.
		checkMatch(t, false, report, `TEXT.*example.com/a.g`)
	})
}

// shownDocument returns the first shown document matching the URI prefix.
// It may be nil.
//
// TODO(adonovan): the integration test framework
// needs a way to reset ShownDocuments so they don't
// accumulate, necessitating the fragile prefix hack.
func shownDocument(t *testing.T, env *Env, prefix string) *protocol.ShowDocumentParams {
	t.Helper()
	var shown []*protocol.ShowDocumentParams
	env.Await(ShownDocuments(&shown))
	var first *protocol.ShowDocumentParams
	for _, sd := range shown {
		if strings.HasPrefix(sd.URI, prefix) {
			if first != nil {
				t.Errorf("got multiple showDocument requests: %#v", shown)
				break
			}
			first = sd
		}
	}
	return first
}

// get fetches the content of a document over HTTP.
func get(t *testing.T, url string) []byte {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	got, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// checkMatch asserts that got matches (or doesn't match, if !want) the pattern.
func checkMatch(t *testing.T, want bool, got []byte, pattern string) {
	t.Helper()
	if regexp.MustCompile(pattern).Match(got) != want {
		if want {
			t.Errorf("input did not match wanted pattern %q; got:\n%s", pattern, got)
		} else {
			t.Errorf("input matched unwanted pattern %q; got:\n%s", pattern, got)
		}
	}
}
