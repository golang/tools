// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package cache

import (
	"os"
	"testing"

	"github.com/google/go-cmp/cmp"
	"golang.org/x/sync/errgroup"
	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/gopls/internal/file"
	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/gopls/internal/util/bug"
	"golang.org/x/tools/internal/testenv"
)

func TestMain(m *testing.M) {
	bug.PanicOnBugs = true
	os.Exit(m.Run())
}

func TestMatchingPortsStdlib(t *testing.T) {
	// This test checks that we don't encounter a bug when matching ports, and
	// sanity checks that the optimization to use trimmed/fake file content
	// before delegating to go/build.Context.MatchFile does not affect
	// correctness.
	if testing.Short() {
		t.Skip("skipping in short mode: takes to long on slow file systems")
	}

	testenv.NeedsTool(t, "go")

	// Load, parse and type-check the program.
	cfg := &packages.Config{
		Mode:  packages.LoadFiles,
		Tests: true,
	}
	pkgs, err := packages.Load(cfg, "std", "cmd")
	if err != nil {
		t.Fatal(err)
	}

	var g errgroup.Group
	packages.Visit(pkgs, nil, func(pkg *packages.Package) {
		for _, f := range pkg.CompiledGoFiles {
			f := f
			g.Go(func() error {
				content, err := os.ReadFile(f)
				// We report errors via t.Error, not by returning,
				// so that a single test can report multiple test failures.
				if err != nil {
					t.Errorf("failed to read %s: %v", f, err)
					return nil
				}
				fh := makeFakeFileHandle(protocol.URIFromPath(f), content)
				fastPorts := matchingPreferredPorts(t, fh, true)
				slowPorts := matchingPreferredPorts(t, fh, false)
				if diff := cmp.Diff(fastPorts, slowPorts); diff != "" {
					t.Errorf("%s: ports do not match (-trimmed +untrimmed):\n%s", f, diff)
					return nil
				}
				return nil
			})
		}
	})
	g.Wait()
}

func matchingPreferredPorts(tb testing.TB, fh file.Handle, trimContent bool) map[port]unit {
	content, err := fh.Content()
	if err != nil {
		tb.Fatal(err)
	}
	if trimContent {
		content = trimContentForPortMatch(content)
	}
	path := fh.URI().Path()
	matching := make(map[port]unit)
	for _, port := range preferredPorts {
		if port.matches(path, content) {
			matching[port] = unit{}
		}
	}
	return matching
}

func BenchmarkMatchingPreferredPorts(b *testing.B) {
	// Copy of robustio_posix.go
	const src = `
// Copyright 2022 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build unix
// +build unix

package robustio

import (
	"os"
	"syscall"
	"time"
)

func getFileID(filename string) (FileID, time.Time, error) {
	fi, err := os.Stat(filename)
	if err != nil {
		return FileID{}, time.Time{}, err
	}
	stat := fi.Sys().(*syscall.Stat_t)
	return FileID{
		device: uint64(stat.Dev), // (int32 on darwin, uint64 on linux)
		inode:  stat.Ino,
	}, fi.ModTime(), nil
}
`
	fh := makeFakeFileHandle("file:///path/to/test/file.go", []byte(src))
	for i := 0; i < b.N; i++ {
		_ = matchingPreferredPorts(b, fh, true)
	}
}
