// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package web_test

import (
	"testing"

	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/gopls/internal/protocol/command"
	"golang.org/x/tools/gopls/internal/settings"
	. "golang.org/x/tools/gopls/internal/test/integration"
)

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

		// Invoke the "Browse free symbols" code
		// action to start the server.
		loc := env.RegexpSearch("a/a.go", "«((?:.|\n)*)»")
		actions, err := env.Editor.CodeAction(env.Ctx, loc, nil, protocol.CodeActionUnknownTrigger)
		if err != nil {
			t.Fatalf("CodeAction: %v", err)
		}
		action, err := codeActionByKind(actions, settings.GoFreeSymbols)
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
