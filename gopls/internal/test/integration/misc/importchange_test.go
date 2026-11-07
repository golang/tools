// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package misc

import (
	"testing"

	"golang.org/x/tools/gopls/internal/protocol"
	. "golang.org/x/tools/gopls/internal/test/integration"
)

// TestImportChange checks that repeated edits to an import path cause
// go/packages to reload metadata, both when the file already has imports and
// when it only has a package clause.
//
// Like TestReloadOnlyOnce in the workspace package, this test depends on the
// structure of gopls' log messages, since metadata reloads have no other stable
// observable signal. It is nevertheless important for guarding against import
// edits that fail to reload metadata.
func TestImportChange(t *testing.T) {
	const (
		loadLog = `packages\.Load #\d+\n`
		first   = `import _ "container/list"`
		second  = `import _ "container/heap"`
	)
	for name, files := range map[string]string{
		"existing_imports": `-- go.mod --
module example.com

go 1.24
-- p.go --
package p

import (
	"bytes"
	"io"
	"math"
)

var _, _, _ = bytes.NewBuffer, io.EOF, math.Pi
`,
		"package_only": `-- go.mod --
module example.com

go 1.24
-- p.go --
package p
`,
	} {
		t.Run(name, func(t *testing.T) {
			WithOptions(Settings{"diagnosticsDelay": "0s"}).Run(t, files, func(t *testing.T, env *Env) {
				env.OpenFile("p.go")
				env.AfterChange(NoDiagnostics(ForFile("p.go")))

				// Every fixture has its package clause on the first line, so insert an
				// unused import on the second line, and edit that line thereafter.
				env.EditBuffer("p.go", protocol.TextEdit{
					Range:   protocol.Range{Start: protocol.Position{Line: 1}, End: protocol.Position{Line: 1}},
					NewText: first + "\n",
				})
				env.AfterChange(NoDiagnostics(ForFile("p.go")))
				start := env.RegexpSearch("p.go", first).Range.Start
				importRange := protocol.Range{Start: start, End: protocol.Position{Line: start.Line + 1}}

				for i := 0; i < 4; i++ {
					path := first
					if i%2 == 0 {
						path = second
					}
					env.EditBuffer("p.go", protocol.TextEdit{Range: importRange, NewText: path + "\n"})
					env.AfterChange(NoDiagnostics(ForFile("p.go")))
					env.Await(LogMatching(protocol.Info, loadLog, i+3, false))
				}
			})
		})
	}
}
