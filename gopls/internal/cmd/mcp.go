// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package cmd

import (
	"context"
	"flag"
	"fmt"

	"golang.org/x/tools/gopls/internal/lsprpc"
	"golang.org/x/tools/gopls/internal/mcp"
)

type headlessMCP struct {
	app *Application

	Address string `flag:"listen" help:"the address on which to run the mcp server"`
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
	// Start a new in-process gopls session and create a fake client
	// to connect to it.
	cli, sess, err := m.app.connect(ctx)
	if err != nil {
		return err
	}
	defer cli.terminate(ctx)

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
		return mcp.StartStdIO(ctx, sess, cli.server)
	}
}
