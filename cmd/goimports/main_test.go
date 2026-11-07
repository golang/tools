// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"log"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/tools/internal/modindex"
	"golang.org/x/tools/internal/testenv"
	"golang.org/x/tools/txtar"
)

const cache = `
-- github.com/nbutton23/zxcvbn-go@v0.0.0-20210217022336-fa2cb2858354/zxcvnb.go --
package zxcvbn
func PasswordStrength(password string, userInputs []string, filters ...func(match.Matcher) bool) {}
-- github.com/klauspost/compress/zstd@v1.16.7/zstd.go --
package zstd
func EncoderLevelFromString(s string) (EncoderLevel, error) {}
-- github.com/containerd/stargz-snapshotter@v0.11.3/estargz/zstdchunked/zstdchunked.go --
package zstdchunked
const FooterSize = 4
-- github.com/nbutton23/zxcvbn-go@v0.0.0-20210217022336-fa2cb2858354/utils/math/mathutils.go --
package zxcvbnmath
func NChoseK(n, k float64) float64 {return 0}
`
const src = `package p

var _ = zxcvbnmath.NChoseK
var _ = zxcvbn.PasswordStrength
var _ = zstdchunked.FooterSize
var _ = zstd.EncoderLevelFromString
`
const want = `package p

import (
	"github.com/containerd/stargz-snapshotter/estargz/zstdchunked"
	"github.com/klauspost/compress/zstd"
	"github.com/nbutton23/zxcvbn-go"
	zxcvbnmath "github.com/nbutton23/zxcvbn-go/utils/math"
)

var _ = zxcvbnmath.NChoseK
var _ = zxcvbn.PasswordStrength
var _ = zstdchunked.FooterSize
var _ = zstd.EncoderLevelFromString
`

// TestCmd runs the command on a module cache with
// an index but no cached files. The test sets up
// the environment and calls main(). The alternative
// approach, to run the binary as a subprocess, does
// not work, as building the repository may require
// getting modules from the network, and not all
// the builders have network access.
func TestCmd(t *testing.T) {
	testenv.NeedsExec(t)
	log.SetFlags(log.Lshortfile)
	dir := t.TempDir()
	modindex.IndexDir = filepath.Join(modindex.IndexDir, "goimports")
	if err := os.MkdirAll(modindex.IndexDir, 0777); err != nil {
		t.Fatalf("failed to create index dir: %v", err)
	}
	// write the cache
	modcache := filepath.Join(dir, "/mod")
	if err := os.MkdirAll(modcache, 0755); err != nil {
		t.Fatalf("failed to create modcache: %v", err)
	}
	archive := txtar.Parse([]byte(cache))
	fsys, err := txtar.FS(archive)
	if err != nil {
		t.Fatalf("failed to create fsys: %v", err)
	}
	if err := os.CopyFS(modcache, fsys); err != nil {
		t.Fatalf("failed to copy fsys: %v", err)
	}
	// create the index
	_, err = modindex.Update(modcache)
	if err != nil {
		t.Fatalf("failed to create index: %v", err)
	}
	// now remove all the files, so go imports uses only the index
	if err := os.RemoveAll(filepath.Join(modcache, "github.com")); err != nil {
		t.Fatalf("failed to remove github.com: %v", err)
	}
	// write the test file
	fname := filepath.Join(dir, "main.go")
	if err := os.WriteFile(fname, []byte(src), 0644); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}

	os.Args = append(os.Args, "-w", fname)
	// need to change the current environment
	os.Setenv("GOMODCACHE", modcache)
	gofmtMain()

	got, err := os.ReadFile(fname)
	if err != nil {
		t.Fatalf("failed to read file: %v", err)
	}
	if string(got) != want {
		t.Errorf("goimports -w %s failed: got %s, want %s", fname, got, want)
	}
}
