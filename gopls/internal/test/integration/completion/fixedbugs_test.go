// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package completion

import (
	"testing"

	. "golang.org/x/tools/gopls/internal/test/integration"
)

func TestPackageCompletionCrash_Issue68169(t *testing.T) {
	// This test reproduces the scenario of golang/go#68169, a crash in
	// completion.Selection.Suffix.
	//
	// The file content here is extracted from the issue.
	const files = `
-- go.mod --
module example.com

go 1.18
-- playdos/play.go --
package  
`

	Run(t, files, func(t *testing.T, env *Env) {
		env.OpenFile("playdos/play.go")
		// Previously, this call would crash gopls as it was incorrectly computing
		// the surrounding completion suffix.
		completions := env.Completion(env.RegexpSearch("playdos/play.go", "package  ()"))
		if len(completions.Items) == 0 {
			t.Fatal("Completion() returned empty results")
		}
		// Sanity check: we should get package clause completion.
		if got, want := completions.Items[0].Label, "package playdos"; got != want {
			t.Errorf("Completion()[0].Label == %s, want %s", got, want)
		}
	})
}
