// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package web_test

import (
	"encoding/json"
	"runtime"
	"testing"

	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/gopls/internal/protocol/command"
	. "golang.org/x/tools/gopls/internal/test/integration"
	"golang.org/x/tools/internal/testenv"
)

// TestFlightRecorder checks that the flight recorder is minimally functional.
func TestFlightRecorder(t *testing.T) {
	// The usual UNIX mechanisms cause timely termination of the
	// cmd/trace process, but this doesn't happen on Windows,
	// leading to CI failures because of process->file locking.
	// Rather than invent a complex mechanism, skip the test:
	// this feature is only for gopls developers anyway.
	// Better long term solutions are CL 677262 and issue #66843.
	if runtime.GOOS == "windows" {
		t.Skip("not reliable on windows")
	}
	testenv.NeedsGo1Point(t, 25)

	const files = `
-- go.mod --
module example.com

-- a/a.go --
package a

const A = 1
`

	Run(t, files, func(t *testing.T, env *Env) {
		env.OpenFile("a/a.go")

		// Start the debug server.
		var result command.DebuggingResult
		env.ExecuteCommand(&protocol.ExecuteCommandParams{
			Command:   command.StartDebugging.String(),
			Arguments: []json.RawMessage{json.RawMessage("{}")}, // no args -> pick port
		}, &result)
		uri := result.URLs[0]
		t.Logf("StartDebugging: URLs[0] = %s", uri)

		// Check the debug server page is sensible.
		doc1 := get(t, uri)
		checkMatch(t, true, doc1, "Gopls server information")
		checkMatch(t, true, doc1, `<a href="/flightrecorder">Flight recorder</a>`)

		// "Click" the Flight Recorder link.
		// It should redirect to the web server
		// of a "go tool trace" process.
		// The resulting web page is entirely programmatic,
		// so we check for an arbitrary expected symbol.
		doc2 := get(t, uri+"/flightrecorder")
		checkMatch(t, true, doc2, `onTraceViewerImportFail`)
	})
}
