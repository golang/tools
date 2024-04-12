// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package misc

import (
	"strings"
	"testing"

	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/gopls/internal/test/integration"
	. "golang.org/x/tools/gopls/internal/test/integration"
	"golang.org/x/tools/gopls/internal/util/bug"
)

func TestMain(m *testing.M) {
	bug.PanicOnBugs = true
	integration.Main(m)
}

// TestDocumentURIFix ensures that a DocumentURI supplied by the
// client is subject to the "fixing" operation documented at
// [protocol.DocumentURI.UnmarshalText]. The details of the fixing are
// tested in the protocol package; here we aim to test only that it
// occurs at all.
func TestDocumentURIFix(t *testing.T) {
	const mod = `
-- go.mod --
module testdata
go 1.18

-- a.go --
package a

const K = 1
`
	Run(t, mod, func(t *testing.T, env *Env) {
		env.OpenFile("a.go")
		loc := env.RegexpSearch("a.go", "K")
		path := strings.TrimPrefix(string(loc.URI), "file://") // (absolute)

		check := func() {
			t.Helper()
			t.Logf("URI = %s", loc.URI)
			content, _ := env.Hover(loc) // must succeed
			if content == nil || !strings.Contains(content.Value, "const K") {
				t.Errorf("wrong content: %#v", content)
			}
		}

		// Regular URI (e.g. file://$TMPDIR/TestDocumentURIFix/default/work/a.go)
		check()

		// URL-encoded path (e.g. contains %2F instead of last /)
		loc.URI = protocol.DocumentURI("file://" + strings.Replace(path, "/a.go", "%2Fa.go", 1))
		check()

		// We intentionally do not test further cases (e.g.
		// file:// without a third slash) as it would quickly
		// get bogged down in irrelevant details of the
		// fake editor's own handling of URIs.
	})
}
