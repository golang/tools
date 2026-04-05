// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package bench

import (
	"testing"

	. "golang.org/x/tools/gopls/internal/test/integration"
	"golang.org/x/tools/gopls/internal/test/integration/fake"
)

func BenchmarkSemanticTokens(b *testing.B) {
	env := getRepo(b, "tools").newEnv(b, fake.EditorConfig{
		Settings: map[string]any{
			"semanticTokens": true,
		},
	}, "SemanticTokens", false)

	env.Await(InitialWorkspaceLoad)

	env.AfterChange()

	const path = "internal/lsp/cache/snapshot.go"
	env.OpenFile(path)

	for b.Loop() {
		env.SemanticTokensFull(path)
	}
}
