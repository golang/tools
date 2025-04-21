// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package web_test

// This file defines web server testing utilities.

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
	"testing"

	"golang.org/x/tools/gopls/internal/protocol"
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

// codeActionByKind returns the first action of (exactly) the specified kind, or an error.
func codeActionByKind(actions []protocol.CodeAction, kind protocol.CodeActionKind) (*protocol.CodeAction, error) {
	for _, act := range actions {
		if act.Kind == kind {
			return &act, nil
		}
	}
	return nil, fmt.Errorf("can't find action with kind %s, only %#v", kind, actions)
}
