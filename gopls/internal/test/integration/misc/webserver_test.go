// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package misc

import (
	"html"
	"io"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/gopls/internal/protocol/command"
	. "golang.org/x/tools/gopls/internal/test/integration"
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

// EOF
`
	Run(t, files, func(t *testing.T, env *Env) {
		// Assert that the HTML page contains the expected const declaration.
		// (We may need to make allowances for HTML markup.)
		uri1 := viewPkgDoc(t, env, "a/a.go")
		doc1 := get(t, uri1)
		checkMatch(t, true, doc1, "const A =.*1")

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
		openURL := html.UnescapeString(string(rx.FindSubmatch(doc2)[1]))

		// Fetch the document. Its result isn't important,
		// but it must have the side effect of another showDocument
		// downcall, this time for a "file:" URL, causing the
		// client editor to navigate to the source file.
		t.Log("extracted /open URL", openURL)
		get(t, openURL)

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
	const files = `
-- go.mod --
module example.com

-- a/a.go --
package a

var A, b = 0, 0

func ExportedFunc()
func unexportedFunc()
type ExportedType int
type unexportedType int
`
	Run(t, files, func(t *testing.T, env *Env) {
		uri1 := viewPkgDoc(t, env, "a/a.go")
		doc1 := get(t, uri1)
		// (Ideally our code rendering would also
		// eliminate unexported symbols...)
		// TODO(adonovan): when we emit var anchors,
		// check that #A exists and #b does not.
		checkMatch(t, true, doc1, "var A, b = .*0.*0")

		// Unexported funcs/types/... must still be discarded.
		checkMatch(t, true, doc1, "ExportedFunc")
		checkMatch(t, false, doc1, "unexportedFunc")
		checkMatch(t, true, doc1, "ExportedType")
		checkMatch(t, false, doc1, "unexportedType")
	})
}

// viewPkgDoc invokes the "View package documention" code action in
// the specified file. It returns the URI of the document, or fails
// the test.
func viewPkgDoc(t *testing.T, env *Env, filename string) protocol.URI {
	env.OpenFile(filename)

	// Invoke the "View package documentation" code
	// action to start the server.
	var docAction *protocol.CodeAction
	actions := env.CodeAction(filename, nil)
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
