// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package cmd

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"sync"
	"time"

	"golang.org/x/tools/gopls/internal/filewatcher"
	"golang.org/x/tools/gopls/internal/lsprpc"
	"golang.org/x/tools/gopls/internal/mcp"
	"golang.org/x/tools/gopls/internal/protocol"
)

type headlessMCP struct {
	app *Application

	Address  string `flag:"listen" help:"the address on which to run the mcp server"`
	Logfile  string `flag:"logfile" help:"filename to log to; if unset, logs to stderr"`
	RPCTrace bool   `flag:"rpc.trace" help:"print MCP rpc traces; cannot be used with -listen"`
}

func (m *headlessMCP) Name() string      { return "mcp" }
func (m *headlessMCP) Parent() string    { return m.app.Name() }
func (m *headlessMCP) Usage() string     { return "[mcp-flags]" }
func (m *headlessMCP) ShortHelp() string { return "start the gopls MCP server in headless mode" }

func (m *headlessMCP) DetailedHelp(f *flag.FlagSet) {
	fmt.Fprint(f.Output(), `
Starts the gopls MCP server in headless mode, without needing an LSP client.
Starts the server over stdio or sse with http, depending on whether the listen flag is provided.

Examples:
  $ gopls mcp -listen=localhost:3000
  $ gopls mcp  //start over stdio
`)
	printFlagDefaults(f)
}

func (m *headlessMCP) Run(ctx context.Context, args ...string) error {
	if m.Address != "" && m.RPCTrace {
		// There's currently no way to plumb logging instrumentation into the SSE
		// transport that is created on connections to the HTTP handler, so we must
		// disallow the -rpc.trace flag when using -listen.
		return fmt.Errorf("-listen is incompatible with -rpc.trace")
	}
	if m.Logfile != "" {
		f, err := os.Create(m.Logfile)
		if err != nil {
			return fmt.Errorf("opening logfile: %v", err)
		}
		log.SetOutput(f)
		defer f.Close()
	}

	// Start a new in-process gopls session and create a fake client
	// to connect to it.
	cli, sess, err := m.app.connect(ctx)
	if err != nil {
		return err
	}
	defer cli.terminate(ctx)

	var (
		queueMu  sync.Mutex
		queue    []protocol.FileEvent
		nonempty = make(chan struct{}) // receivable when len(queue) > 0
		stop     = make(chan struct{}) // closed when Run returns
	)
	defer close(stop)

	// This goroutine forwards file change events to the LSP server.
	go func() {
		for {
			select {
			case <-stop:
				return
			case <-nonempty:
				queueMu.Lock()
				q := queue
				queue = nil
				queueMu.Unlock()

				if len(q) > 0 {
					if err := cli.server.DidChangeWatchedFiles(ctx, &protocol.DidChangeWatchedFilesParams{
						Changes: q,
					}); err != nil {
						log.Printf("failed to notify changed files: %v", err)
					}
				}

			}
		}
	}()

	w, err := filewatcher.New(500*time.Millisecond, nil, func(events []protocol.FileEvent, err error) {
		if err != nil {
			log.Printf("watch error: %v", err)
			return
		}

		if len(events) == 0 {
			return
		}

		// Since there is no promise [protocol.Server.DidChangeWatchedFiles]
		// will return immediately, we should buffer the captured events and
		// sent them whenever available in a separate go routine.
		queueMu.Lock()
		queue = append(queue, events...)
		queueMu.Unlock()

		select {
		case nonempty <- struct{}{}:
		default:
		}
	})
	if err != nil {
		return err
	}
	defer w.Close()

	// TODO(hxjiang): replace this with LSP initial param workspace root.
	dir, err := os.Getwd()
	if err != nil {
		return err
	}
	if err := w.WatchDir(dir); err != nil {
		return err
	}

	// Send a SessionStart event to trigger creation of an http handler.
	if m.Address != "" {
		countHeadlessMCPSSE.Inc()
		// Specify a channel size of two so that the send operations are
		// non-blocking.
		eventChan := make(chan lsprpc.SessionEvent, 2)
		go func() {
			eventChan <- lsprpc.SessionEvent{
				Session: sess,
				Type:    lsprpc.SessionStart,
				Server:  cli.server,
			}
		}()
		defer func() {
			eventChan <- lsprpc.SessionEvent{
				Session: sess,
				Type:    lsprpc.SessionEnd,
				Server:  cli.server,
			}
		}()

		return mcp.Serve(ctx, m.Address, eventChan, false)
	} else {
		countHeadlessMCPStdIO.Inc()
		var rpcLog io.Writer
		if m.RPCTrace {
			rpcLog = log.Writer() // possibly redirected by -logfile above
		}
		log.Printf("Listening for MCP messages on stdin...")
		return mcp.StartStdIO(ctx, sess, cli.server, rpcLog)
	}
}
