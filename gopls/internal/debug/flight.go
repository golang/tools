// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build go1.25

package debug

import (
	"bufio"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/trace"
	"strings"
	"sync"
	"syscall"
	"time"
)

var (
	traceviewersMu sync.Mutex
	traceviewers   []*os.Process

	kill        = (*os.Process).Kill // windows, plan9; UNIX impl kills whole process group
	sysProcAttr syscall.SysProcAttr  // UNIX configuration to create process group
)

// KillTraceViewers kills all "go tool trace" processes started by
// /flightrecorder requests, for use in tests (see #74668).
func KillTraceViewers() {
	traceviewersMu.Lock()
	for _, p := range traceviewers {
		kill(p) // ignore error
	}
	traceviewers = nil
	traceviewersMu.Unlock()
}

// The FlightRecorder is a global resource, so create at most one per process.
var getRecorder = sync.OnceValues(func() (*trace.FlightRecorder, error) {
	fr := trace.NewFlightRecorder(trace.FlightRecorderConfig{
		// half a minute is usually enough to know "what just happened?"
		MinAge: 30 * time.Second,
	})
	if err := fr.Start(); err != nil {
		return nil, err
	}
	return fr, nil
})

func startFlightRecorder() (http.HandlerFunc, error) {
	fr, err := getRecorder()
	if err != nil {
		return nil, err
	}

	// Return a handler that writes the most recent flight record,
	// starts a trace viewer server, and redirects to it.
	return func(w http.ResponseWriter, r *http.Request) {
		errorf := func(format string, args ...any) {
			msg := fmt.Sprintf(format, args...)
			http.Error(w, msg, http.StatusInternalServerError)
		}

		// Write the most recent flight record into a temp file.
		f, err := os.CreateTemp("", "flightrecord")
		if err != nil {
			errorf("can't create temp file for flight record: %v", err)
			return
		}
		if _, err := fr.WriteTo(f); err != nil {
			f.Close() // ignore error
			errorf("failed to write flight record: %s", err)
			return
		}
		if err := f.Close(); err != nil {
			errorf("failed to close flight record: %s", err)
			return
		}
		tracefile, err := filepath.Abs(f.Name())
		if err != nil {
			errorf("can't absolutize name of trace file: %v", err)
			return
		}

		// Run 'go tool trace' to start a new trace-viewer
		// web server process. It will run until gopls terminates.
		// (It would be nicer if we could just link it in; see #66843.)
		cmd := exec.Command("go", "tool", "trace", tracefile)
		cmd.SysProcAttr = &sysProcAttr

		// Don't connect trace's std{out,err} to our os.Stderr directly,
		// otherwise the child may outlive the parent in tests,
		// and 'go test' will complain about unclosed pipes.
		// Instead, interpose a pipe that will close when gopls exits.
		// See CL 677262 for a better solution (a cmd/trace flag).
		// (#66843 is of course better still.)
		// Also, this notifies us of the server's readiness and URL.
		urlC := make(chan string)
		{
			r, w, err := os.Pipe()
			if err != nil {
				errorf("can't create pipe: %v", err)
				return
			}
			go func() {
				// Copy from the pipe to stderr,
				// keeping an eye out for the "listening on URL" string.
				scan := bufio.NewScanner(r)
				for scan.Scan() {
					line := scan.Text()
					if _, url, ok := strings.Cut(line, "Trace viewer is listening on "); ok {
						urlC <- url
					}
					fmt.Fprintln(os.Stderr, line)
				}
				if err := scan.Err(); err != nil {
					log.Printf("reading from pipe to cmd/trace: %v", err)
				}
			}()
			cmd.Stderr = w
			cmd.Stdout = w
		}

		// Suppress the usual cmd/trace behavior of opening a new
		// browser tab by setting BROWSER to /usr/bin/true (a no-op).
		cmd.Env = append(os.Environ(), "BROWSER=true")
		if err := cmd.Start(); err != nil {
			errorf("failed to start trace server: %s", err)
			return
		}

		// Save the process so we can kill it when tests finish.
		traceviewersMu.Lock()
		traceviewers = append(traceviewers, cmd.Process)
		traceviewersMu.Unlock()

		// Some of the CI builders can be quite heavily loaded.
		// Give them an extra grace period.
		timeout := 10 * time.Second
		if os.Getenv("GO_BUILDER_NAME") != "" {
			timeout = 1 * time.Minute
		}

		select {
		case addr := <-urlC:
			// Success! Send a redirect to the new location.
			// (This URL bypasses the help screen at /.)
			http.Redirect(w, r, addr+"/trace?view=proc", 302)

		case <-r.Context().Done():
			errorf("canceled")

		case <-time.After(timeout):
			errorf("trace viewer failed to start within %v", timeout)
		}
	}, nil
}
