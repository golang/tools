// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package gocommand_test

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sync/errgroup"
	"golang.org/x/tools/internal/gocommand"
	"golang.org/x/tools/internal/testenv"
)

func TestGoVersion(t *testing.T) {
	testenv.NeedsTool(t, "go")

	inv := gocommand.Invocation{
		Verb: "version",
	}
	gocmdRunner := &gocommand.Runner{}
	if _, err := gocmdRunner.Run(context.Background(), inv); err != nil {
		t.Error(err)
	}
}

// This is not a test of go/packages at all: it's a test of whether it
// is possible to delete the directory used by go list once it has
// finished. It is intended to evaluate the hypothesis (to explain
// issue #71544) that the go command, on Windows, occasionally fails
// to release all its handles to the temporary directory even when it
// should have finished.
//
// If this test ever fails, the combination of the gocommand package
// and the go command itself has a bug; this has been observed (#73503).
func TestRmdirAfterGoList_Runner(t *testing.T) {
	t.Skip("flaky; see https://github.com/golang/go/issues/73736#issuecomment-2885407104")

	testRmdirAfterGoList(t, func(ctx context.Context, dir string) {
		var runner gocommand.Runner
		stdout, stderr, friendlyErr, err := runner.RunRaw(ctx, gocommand.Invocation{
			Verb:       "list",
			Args:       []string{"-json", "example.com/p"},
			WorkingDir: dir,
		})
		if ctx.Err() != nil {
			return // don't report error if canceled
		}
		if err != nil || friendlyErr != nil {
			t.Fatalf("go list failed: %v, %v (stdout=%s stderr=%s)",
				err, friendlyErr, stdout, stderr)
		}
	})
}

// TestRmdirAfterGoList_Direct is a variant of
// TestRmdirAfterGoList_Runner that executes go list directly, to
// control for the substantial logic of the gocommand package.
//
// If this test ever fails, the go command itself has a bug; as of May
// 2025 this has never been observed.
func TestRmdirAfterGoList_Direct(t *testing.T) {
	testRmdirAfterGoList(t, func(ctx context.Context, dir string) {
		cmd := exec.Command("go", "list", "-json", "example.com/p")
		cmd.Dir = dir
		cmd.Stdout = new(strings.Builder)
		cmd.Stderr = new(strings.Builder)
		err := cmd.Run()
		if ctx.Err() != nil {
			return // don't report error if canceled
		}
		if err != nil {
			t.Fatalf("go list failed: %v (stdout=%s stderr=%s)",
				err, cmd.Stdout, cmd.Stderr)
		}
	})
}

func testRmdirAfterGoList(t *testing.T, f func(ctx context.Context, dir string)) {
	testenv.NeedsExec(t)

	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "p"), 0777); err != nil {
		t.Fatalf("mkdir p: %v", err)
	}

	// Create a go.mod file and 100 trivial Go files for the go command to read.
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com"), 0666); err != nil {
		t.Fatal(err)
	}
	for i := range 100 {
		filename := filepath.Join(dir, fmt.Sprintf("p/%d.go", i))
		if err := os.WriteFile(filename, []byte("package p"), 0666); err != nil {
			t.Fatal(err)
		}
	}

	g, ctx := errgroup.WithContext(context.Background())
	for range 10 {
		g.Go(func() error {
			f(ctx, dir)
			// Return an error so that concurrent invocations are canceled.
			return fmt.Errorf("oops")
		})
	}
	g.Wait() // ignore expected error

	// This is the critical operation.
	if err := os.RemoveAll(dir); err != nil {
		t.Errorf("failed to remove temp dir: %v", err)
		// List the contents of the directory, for clues.
		filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			t.Log(path, d, err)
			return nil
		})
	}
}
