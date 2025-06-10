// Copyright 2018 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package cmd

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"time"

	"golang.org/x/sync/errgroup"
	"golang.org/x/tools/gopls/internal/cache"
	"golang.org/x/tools/gopls/internal/debug"
	"golang.org/x/tools/gopls/internal/lsprpc"
	"golang.org/x/tools/gopls/internal/mcp"
	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/internal/fakenet"
	"golang.org/x/tools/internal/jsonrpc2"
	"golang.org/x/tools/internal/tool"
)

// Serve is a struct that exposes the configurable parts of the LSP and MCP
// server as flags, in the right form for tool.Main to consume.
type Serve struct {
	Logfile     string        `flag:"logfile" help:"filename to log to. if value is \"auto\", then logging to a default output file is enabled"`
	Mode        string        `flag:"mode" help:"no effect"`
	Port        int           `flag:"port" help:"port on which to run gopls for debugging purposes"`
	Address     string        `flag:"listen" help:"address on which to listen for remote connections. If prefixed by 'unix;', the subsequent address is assumed to be a unix domain socket. Otherwise, TCP is used."`
	IdleTimeout time.Duration `flag:"listen.timeout" help:"when used with -listen, shut down the server when there are no connected clients for this duration"`
	Trace       bool          `flag:"rpc.trace" help:"print the full rpc trace in lsp inspector format"`
	Debug       string        `flag:"debug" help:"serve debug information on the supplied address"`

	RemoteListenTimeout time.Duration `flag:"remote.listen.timeout" help:"when used with -remote=auto, the -listen.timeout value used to start the daemon"`
	RemoteDebug         string        `flag:"remote.debug" help:"when used with -remote=auto, the -debug value used to start the daemon"`
	RemoteLogfile       string        `flag:"remote.logfile" help:"when used with -remote=auto, the -logfile value used to start the daemon"`

	// MCP Server related configurations.
	MCPAddress string `flag:"mcp-listen" help:"experimental: address on which to listen for model context protocol connections. If port is localhost:0, pick a random port in localhost instead."`

	app *Application
}

func (s *Serve) Name() string   { return "serve" }
func (s *Serve) Parent() string { return s.app.Name() }
func (s *Serve) Usage() string  { return "[server-flags]" }
func (s *Serve) ShortHelp() string {
	return "run a server for Go code using the Language Server Protocol"
}
func (s *Serve) DetailedHelp(f *flag.FlagSet) {
	fmt.Fprint(f.Output(), `  gopls [flags] [server-flags]

The server communicates using JSONRPC2 on stdin and stdout, and is intended to be run directly as
a child of an editor process.

server-flags:
`)
	printFlagDefaults(f)
}

func (s *Serve) remoteArgs(network, address string) []string {
	args := []string{"serve",
		"-listen", fmt.Sprintf(`%s;%s`, network, address),
	}
	if s.RemoteDebug != "" {
		args = append(args, "-debug", s.RemoteDebug)
	}
	if s.RemoteListenTimeout != 0 {
		args = append(args, "-listen.timeout", s.RemoteListenTimeout.String())
	}
	if s.RemoteLogfile != "" {
		args = append(args, "-logfile", s.RemoteLogfile)
	}
	return args
}

// Run configures a server based on the flags, and then runs it.
// It blocks until the server shuts down.
func (s *Serve) Run(ctx context.Context, args ...string) error {
	if len(args) > 0 {
		return tool.CommandLineErrorf("server does not take arguments, got %v", args)
	}

	di := debug.GetInstance(ctx)
	isDaemon := s.Address != "" || s.Port != 0
	if di != nil {
		closeLog, err := di.SetLogFile(s.Logfile, isDaemon)
		if err != nil {
			return err
		}
		defer closeLog()
		di.ServerAddress = s.Address
		di.Serve(ctx, s.Debug)
	}

	var ss jsonrpc2.StreamServer

	// eventChan is used by the LSP server to send session lifecycle events
	// (creation, exit) to the MCP server. The sender must ensure that an exit
	// event for a given LSP session ID is sent after its corresponding creation
	// event.
	var eventChan chan lsprpc.SessionEvent

	if s.app.Remote != "" {
		var err error
		ss, err = lsprpc.NewForwarder(s.app.Remote, s.remoteArgs)
		if err != nil {
			return fmt.Errorf("creating forwarder: %w", err)
		}
	} else {
		if s.MCPAddress != "" {
			eventChan = make(chan lsprpc.SessionEvent)
		}
		ss = lsprpc.NewStreamServer(cache.New(nil), isDaemon, eventChan, s.app.options)
	}

	group, ctx := errgroup.WithContext(ctx)
	// Indicate success by a special error so that successful termination
	// of one server causes cancellation of the other.
	success := errors.New("success")

	// Start MCP server.
	if eventChan != nil {
		group.Go(func() (err error) {
			defer func() {
				if err == nil {
					err = success
				}
			}()

			return mcp.Serve(ctx, s.MCPAddress, eventChan, isDaemon)
		})
	}

	// Start LSP server.
	group.Go(func() (err error) {
		defer func() {
			// Once we have finished serving LSP over jsonrpc or stdio,
			// there can be no more session events. Notify the MCP server.
			if eventChan != nil {
				close(eventChan)
			}
			if err == nil {
				err = success
			}
		}()

		var network, addr string
		if s.Address != "" {
			network, addr = lsprpc.ParseAddr(s.Address)
		}
		if s.Port != 0 {
			network = "tcp"
			// TODO(adonovan): should gopls ever be listening on network
			// sockets, or only local ones?
			//
			// Ian says this was added in anticipation of
			// something related to "VS Code remote" that turned
			// out to be unnecessary. So I propose we limit it to
			// localhost, if only so that we avoid the macOS
			// firewall prompt.
			//
			// Hana says: "s.Address is for the remote access (LSP)
			// and s.Port is for debugging purpose (according to
			// the Server type documentation). I am not sure why the
			// existing code here is mixing up and overwriting addr.
			// For debugging endpoint, I think localhost makes perfect sense."
			//
			// TODO(adonovan): disentangle Address and Port,
			// and use only localhost for the latter.
			addr = fmt.Sprintf(":%v", s.Port)
		}

		if addr != "" {
			log.Printf("Gopls LSP daemon: listening on %s network, address %s...", network, addr)
			defer log.Printf("Gopls LSP daemon: exiting")
			return jsonrpc2.ListenAndServe(ctx, network, addr, ss, s.IdleTimeout)
		} else {
			stream := jsonrpc2.NewHeaderStream(fakenet.NewConn("stdio", os.Stdin, os.Stdout))
			if s.Trace && di != nil {
				stream = protocol.LoggingStream(stream, di.LogWriter)
			}
			conn := jsonrpc2.NewConn(stream)
			if err := ss.ServeStream(ctx, conn); errors.Is(err, io.EOF) {
				return nil
			} else {
				return err
			}
		}
	})

	// Wait for all servers to terminate, returning only the first error
	// encountered. Subsequent errors are typically due to context cancellation
	// and are disregarded.
	if err := group.Wait(); err != nil && !errors.Is(err, success) {
		return err
	}
	return nil
}
