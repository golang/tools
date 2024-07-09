// Copyright 2021 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package command_test

import (
	"os"
	"testing"

	"github.com/google/go-cmp/cmp"
	"golang.org/x/tools/gopls/internal/protocol/command/gen"
	"golang.org/x/tools/internal/testenv"
)

// TestGenerated ensures that we haven't forgotten to update command_gen.go.
func TestGenerated(t *testing.T) {
	testenv.NeedsGoPackages(t)
	testenv.NeedsLocalXTools(t)

	onDisk, err := os.ReadFile("command_gen.go")
	if err != nil {
		t.Fatal(err)
	}

	generated, err := gen.Generate()
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(string(generated), string(onDisk)); diff != "" {
		t.Errorf("command_gen.go is stale -- regenerate (-generated +on disk)\n%s", diff)
	}
}
