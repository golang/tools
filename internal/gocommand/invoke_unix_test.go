// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build unix

package gocommand_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"golang.org/x/tools/internal/gocommand"
	"golang.org/x/tools/internal/testenv"
)

// TestCancel_ProcessGroup checks that cancelling a Go command terminates
// subprocesses spawned in its process group (golang/go#81408).
func TestCancel_ProcessGroup(t *testing.T) {
	testenv.NeedsTool(t, "go")

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module cancel.test\n\ngo 1.20\n"), 0666); err != nil {
		t.Fatal(err)
	}

	pidFile := filepath.Join(dir, "child.pid")
	testSrc := fmt.Sprintf(`package main

import (
	"fmt"
	"os"
	"testing"
	"time"
)

func TestSleep(t *testing.T) {
	if err := os.WriteFile(%q, []byte(fmt.Sprint(os.Getpid())), 0666); err != nil {
		t.Fatal(err)
	}
	time.Sleep(30 * time.Second)
}
`, pidFile)
	if err := os.WriteFile(filepath.Join(dir, "sleep_test.go"), []byte(testSrc), 0666); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var runner gocommand.Runner
	errCh := make(chan error, 1)
	go func() {
		_, err := runner.Run(ctx, gocommand.Invocation{
			Verb:       "test",
			Args:       []string{"-v", "."},
			WorkingDir: dir,
		})
		errCh <- err
	}()

	// Wait for the test process to start and write its PID.
	var childPID int
	for delay := time.Millisecond; delay < 10*time.Second; delay *= 2 {
		data, err := os.ReadFile(pidFile)
		if err == nil && len(data) > 0 {
			fmt.Sscanf(string(data), "%d", &childPID)
			if childPID > 0 {
				break
			}
		}
		time.Sleep(delay)
	}
	if childPID == 0 {
		t.Fatal("child test process did not write PID")
	}

	cancel()

	select {
	case err := <-errCh:
		if err == nil {
			t.Fatal("command succeeded unexpectedly, wanted cancellation error")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for command to cancel")
	}

	// Verify that the child test process in the process group was killed.
	dead := false
	for delay := time.Millisecond; delay < 5*time.Second; delay *= 2 {
		err := syscall.Kill(childPID, 0)
		if errors.Is(err, syscall.ESRCH) {
			dead = true
			break
		}
		time.Sleep(delay)
	}
	if !dead {
		t.Errorf("child process %d still alive after cancellation", childPID)
	}
}
