// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package web_test

// This file defines web server testing utilities.

import (
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
	"testing"

	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/gopls/internal/protocol/command"
	"golang.org/x/tools/gopls/internal/test/integration"
	"golang.org/x/tools/gopls/internal/util/bug"
)

func TestMain(m *testing.M) {
	bug.PanicOnBugs = true
	os.Exit(integration.Main(m))
}

// shownDocument returns the first shown document matching the URI prefix.
// It may be nil.
// As a side effect, it clears the list of accumulated shown documents.
func shownDocument(t *testing.T, shown []*protocol.ShowDocumentParams, prefix string) *protocol.ShowDocumentParams {
	t.Helper()
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

// codeActionWebPage returns the URL and content of the page opened by the specified code action.
func codeActionWebPage(t *testing.T, env *integration.Env, kind protocol.CodeActionKind, loc protocol.Location) (string, []byte) {
	actions, err := env.Editor.CodeAction(env.Ctx, loc, nil, protocol.CodeActionUnknownTrigger)
	if err != nil {
		t.Fatalf("CodeAction: %v", err)
	}
	action, err := integration.CodeActionByKind(actions, kind)
	if err != nil {
		t.Fatal(err)
	}
	action, err = env.Editor.ResolveCodeAction(env.Ctx, action)
	if err != nil {
		t.Fatal(err)
	}

	// Execute the command.
	// Its side effect should be a single showDocument request.
	params := &protocol.ExecuteCommandParams{
		Command:   action.Command.Command,
		Arguments: action.Command.Arguments,
	}
	var result command.DebuggingResult
	collectDocs := env.Awaiter.ListenToShownDocuments()
	env.ExecuteCommand(params, &result)
	doc := shownDocument(t, collectDocs(), "http:")
	if doc == nil {
		t.Fatalf("no showDocument call had 'file:' prefix")
	}
	t.Log("showDocument(package doc) URL:", doc.URI)

	return doc.URI, get(t, doc.URI)
}
