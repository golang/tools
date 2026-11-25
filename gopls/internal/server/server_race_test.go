// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package server

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"golang.org/x/tools/gopls/internal/cache"
	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/gopls/internal/protocol/command"
	"golang.org/x/tools/gopls/internal/settings"
	"golang.org/x/tools/internal/testenv"
)

const fakeGovulncheckEnvvar = "GOPLS_TEST_FAKE_GOVULNCHECK"

func TestMain(m *testing.M) {
	// scan.RunGovulncheck executes `os.Args[0] vulncheck ...` as a subprocess.
	// When GOPLS_TEST_FAKE_GOVULNCHECK=1 is set to act as a fake govulncheck
	// subprocess that writes a line to stderr and exits non-zero.
	// scan.RunGovulncheck forwards that stderr through progress.NewWorkDoneWriter
	// (calling client.Progress with *protocol.WorkDoneProgressReport) before
	// returning, which lets the test deterministically pause the async
	// RunGovulncheck callback mid-scan without adding test hooks to production code.
	if os.Getenv(fakeGovulncheckEnvvar) == "1" && len(os.Args) > 1 && os.Args[1] == "vulncheck" {
		fmt.Fprintln(os.Stderr, "fake govulncheck failure")
		os.Exit(1)
	}
	os.Exit(m.Run())
}

func TestServerGetWebConcurrentShutdown(t *testing.T) {
	ctx := t.Context()
	for range 5 {
		client := &mockClient{}
		session := cache.NewSession(ctx, cache.New(nil))
		s := New(session, client, settings.DefaultOptions()).(*server)

		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			w, err := s.getWeb()
			if (w == nil) == (err == nil) {
				t.Errorf("getWeb() = (%v, %v), want exactly one non-nil", w, err)
			}
			if w != nil {
				w.server.Close()
			}
		}()
		go func() {
			defer wg.Done()
			if err := s.Shutdown(ctx); err != nil {
				t.Errorf("Shutdown() failed: %v", err)
			}
		}()
		wg.Wait()
	}
}

func TestServerEventuallyShowMessageConcurrentInitialized(t *testing.T) {
	ctx := t.Context()
	client := &mockClient{}
	session := cache.NewSession(ctx, cache.New(nil))
	s := New(session, client, settings.DefaultOptions()).(*server)

	if _, err := s.Initialize(ctx, &protocol.ParamInitialize{}); err != nil {
		t.Fatalf("Initialize() failed: %v", err)
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for range 50 {
			s.eventuallyShowMessage(ctx, &protocol.ShowMessageParams{
				Type:    protocol.Info,
				Message: "test message",
			})
		}
	}()
	go func() {
		defer wg.Done()
		if err := s.Initialized(ctx, &protocol.InitializedParams{}); err != nil {
			t.Errorf("Initialized() failed: %v", err)
		}
	}()
	wg.Wait()

	s.stateMu.Lock()
	gotNotifications := len(s.notifications)
	s.stateMu.Unlock()
	if gotNotifications != 0 {
		t.Errorf("after Initialized, len(s.notifications) = %d, want 0", gotNotifications)
	}
}

func TestServerConcurrentOptionsAndCommands(t *testing.T) {
	ctx := t.Context()
	client := &mockClient{}
	session := cache.NewSession(ctx, cache.New(nil))
	s := New(session, client, settings.DefaultOptions()).(*server)
	h := &commandHandler{
		s:      s,
		params: &protocol.ExecuteCommandParams{},
	}

	var wg sync.WaitGroup
	wg.Add(3)
	go func() {
		defer wg.Done()
		for range 50 {
			opts := s.Options().Clone()
			opts.TelemetryPrompt = !opts.TelemetryPrompt
			s.SetOptions(opts)
		}
	}()
	go func() {
		defer wg.Done()
		for range 50 {
			if err := h.MaybePromptForTelemetry(ctx); err != nil {
				t.Errorf("MaybePromptForTelemetry() failed: %v", err)
			}
		}
	}()
	go func() {
		defer wg.Done()
		for range 50 {
			err := h.run(ctx, commandConfig{
				progress:      "test",
				progressStyle: settings.WorkDoneProgressStyleLog,
			}, func(context.Context, commandDeps) error {
				return nil
			})
			if err != nil {
				t.Errorf("commandHandler.run() failed: %v", err)
			}
		}
	}()
	wg.Wait()
}

// TestRunGovulncheckInProgressResetOnError verifies that moving the reset of
// runGovulncheckInProgress into the async callback does not leave the flag
// stuck true when RunGovulncheck returns an error before starting the callback.
func TestRunGovulncheckInProgressResetOnError(t *testing.T) {
	ctx := t.Context()
	client := &mockClient{}
	session := cache.NewSession(ctx, cache.New(nil))
	s := New(session, client, settings.DefaultOptions()).(*server)
	h := &commandHandler{
		s:      s,
		params: &protocol.ExecuteCommandParams{Command: "gopls.run_govulncheck"},
	}

	for _, uri := range []protocol.DocumentURI{"", "file:///nonexistent/go.mod"} {
		if _, err := h.RunGovulncheck(ctx, command.VulncheckArgs{URI: uri}); err == nil {
			t.Errorf("RunGovulncheck(%q) = nil, want non-nil error", uri)
		}
		if s.runGovulncheckInProgress.Load() {
			t.Errorf("s.runGovulncheckInProgress.Load() = true after RunGovulncheck(%q) error, want false", uri)
		}
	}
}

// TestRunGovulncheckInProgressDeduplication verifies that
// runGovulncheckInProgress remains true after RunGovulncheck returns its
// progress token, deduplicates concurrent invocations while the async callback
// is still running, and resets to false once the callback completes.
//
// How this works: scan.RunGovulncheck spawns `os.Args[0] vulncheck ...`. With
// GOPLS_TEST_FAKE_GOVULNCHECK=1, TestMain intercepts that subprocess, writes a
// line to stderr, and exits non-zero. scan.RunGovulncheck then forwards stderr
// through progress.NewWorkDoneWriter to client.Progress(*protocol.WorkDoneProgressReport)
// while still inside RunGovulncheck's async callback (before its deferred
// runGovulncheckInProgress.Store(false) runs). Blocking in mockClient.progress
// on that report deterministically holds the async callback open while we check
// the flag and issue a second RunGovulncheck call. After unblocking,
// commandHandler.run sends *protocol.WorkDoneProgressEnd only after the callback
// returns, establishing a happens-before edge for checking the reset to false.
func TestRunGovulncheckInProgressDeduplication(t *testing.T) {
	testenv.NeedsExec(t)
	testenv.NeedsTool(t, "go")
	t.Setenv("GOPACKAGESDRIVER", "off")
	t.Setenv(fakeGovulncheckEnvvar, "1")

	ctx := t.Context()
	dir := t.TempDir()
	modPath := filepath.Join(dir, "go.mod")
	if err := os.WriteFile(modPath, []byte("module example.com\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	modURI := protocol.URIFromPath(modPath)

	inCallback := make(chan struct{})
	releaseCallback := make(chan struct{})
	var reportOnce sync.Once
	unblock := sync.OnceFunc(func() { close(releaseCallback) })
	ended := make(chan protocol.ProgressToken, 2)
	var duplicateMsg atomic.Bool

	client := &mockClient{
		showMessage: func(_ context.Context, params *protocol.ShowMessageParams) error {
			if params.Type == protocol.Info && strings.Contains(params.Message, "already in progress") {
				duplicateMsg.Store(true)
			}
			return nil
		},
		progress: func(_ context.Context, params *protocol.ProgressParams) error {
			switch params.Value.(type) {
			case *protocol.WorkDoneProgressReport:
				reportOnce.Do(func() { close(inCallback) })
				<-releaseCallback
			case *protocol.WorkDoneProgressEnd:
				ended <- params.Token
			}
			return nil
		},
	}

	session := cache.NewSession(ctx, cache.New(nil))
	defer session.Shutdown(ctx)

	s := New(session, client, settings.DefaultOptions()).(*server)
	// WorkDone progress must be enabled so progress.WorkDone.Report forwards
	// writes to client.Progress instead of returning early on a nil token.
	s.progress.SetSupportsWorkDoneProgress(true)

	folder, err := s.newFolder(ctx, protocol.URIFromPath(dir), "test", s.Options())
	if err != nil {
		t.Fatalf("newFolder() failed: %v", err)
	}
	_, _, releaseSnap, err := session.NewView(ctx, folder)
	if err != nil {
		t.Fatalf("NewView() failed: %v", err)
	}
	defer releaseSnap()
	// Run unblock before session.Shutdown waits on active snapshot references.
	defer unblock()

	h := &commandHandler{
		s:      s,
		params: &protocol.ExecuteCommandParams{Command: "gopls.run_govulncheck"},
	}

	res1, err := h.RunGovulncheck(ctx, command.VulncheckArgs{URI: modURI, Pattern: "./..."})
	if err != nil {
		t.Fatalf("first RunGovulncheck() failed: %v", err)
	}
	if res1.Token == nil {
		t.Fatalf("first RunGovulncheck() returned nil progress token")
	}

	select {
	case <-inCallback:
	case <-ctx.Done():
		t.Fatalf("timed out waiting for async RunGovulncheck callback: %v", ctx.Err())
	}

	if !s.runGovulncheckInProgress.Load() {
		t.Errorf("runGovulncheckInProgress = false while async scan is still running, want true")
	}

	res2, err := h.RunGovulncheck(ctx, command.VulncheckArgs{URI: modURI, Pattern: "./..."})
	if err != nil {
		t.Errorf("second RunGovulncheck() failed: %v", err)
	}
	if res2.Token != nil {
		t.Errorf("second RunGovulncheck() started a duplicate scan (token=%v), want nil", res2.Token)
	}
	if !duplicateMsg.Load() {
		t.Errorf("second RunGovulncheck() did not send 'already in progress' ShowMessage")
	}

	unblock()

	waitTokens := map[protocol.ProgressToken]bool{res1.Token: true}
	if res2.Token != nil {
		waitTokens[res2.Token] = true
	}
	for len(waitTokens) > 0 {
		select {
		case tok := <-ended:
			delete(waitTokens, tok)
		case <-ctx.Done():
			t.Fatalf("timed out waiting for WorkDoneProgressEnd for %v: %v", waitTokens, ctx.Err())
		}
	}

	if s.runGovulncheckInProgress.Load() {
		t.Errorf("runGovulncheckInProgress = true after async scan completed, want false")
	}
}
