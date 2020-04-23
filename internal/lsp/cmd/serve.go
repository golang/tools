// Copyright 2018 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package cmd

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"golang.org/x/tools/internal/event"
	"golang.org/x/tools/internal/jsonrpc2"
	"golang.org/x/tools/internal/lsp/cache"
	"golang.org/x/tools/internal/lsp/debug"
	"golang.org/x/tools/internal/lsp/lsprpc"
	"golang.org/x/tools/internal/lsp/protocol"
	"golang.org/x/tools/internal/tool"
)

// Serve is a struct that exposes the configurable parts of the LSP server as
// flags, in the right form for tool.Main to consume.
type Serve struct {
	Logfile     string        `flag:"logfile" help:"filename to log to. if value is \"auto\", then logging to a default output file is enabled"`
	Mode        string        `flag:"mode" help:"no effect"`
	Port        int           `flag:"port" help:"port on which to run gopls for debugging purposes"`
	Address     string        `flag:"listen" help:"address on which to listen for remote connections. If prefixed by 'unix;', the subsequent address is assumed to be a unix domain socket. Otherwise, TCP is used."`
	IdleTimeout time.Duration `flag:"listen.timeout" help:"when used with -listen, shut down the server when there are no connected clients for this duration"`
	Trace       bool          `flag:"rpc.trace" help:"print the full rpc trace in lsp inspector format"`
	Debug       string        `flag:"debug" help:"serve debug information on the supplied address"`

	RemoteListenTimeout time.Duration `flag:"remote.listen.timeout" help:"when used with -remote=auto, the listen.timeout used when auto-starting the remote"`
	RemoteDebug         string        `flag:"remote.debug" help:"when used with -remote=auto, the debug address used when auto-starting the remote"`
	RemoteLogfile       string        `flag:"remote.logfile" help:"when used with -remote=auto, the filename for the remote daemon to log to"`
	DisableExport       bool          `flag:"telemetry.disable" help:"TEMPORARY WORKAROUND: disable telemetry processing entirely. This flag will be removed in the future, once telemetry issues are resolved."`

	app *Application
}

func (s *Serve) Name() string  { return "serve" }
func (s *Serve) Usage() string { return "" }
func (s *Serve) ShortHelp() string {
	return "run a server for Go code using the Language Server Protocol"
}
func (s *Serve) DetailedHelp(f *flag.FlagSet) {
	fmt.Fprint(f.Output(), `
The server communicates using JSONRPC2 on stdin and stdout, and is intended to be run directly as
a child of an editor process.

gopls server flags are:
`)
	f.PrintDefaults()
}

// Run configures a server based on the flags, and then runs it.
// It blocks until the server shuts down.
func (s *Serve) Run(ctx context.Context, args ...string) error {
	if len(args) > 0 {
		return tool.CommandLineErrorf("server does not take arguments, got %v", args)
	}

	// Temporary workaround for golang.org/issues/38042: allow disabling
	// telemetry export.
	if s.DisableExport {
		event.SetExporter(nil)
	}

	di := debug.GetInstance(ctx)
	if di != nil {
		closeLog, err := di.SetLogFile(s.Logfile)
		if err != nil {
			return err
		}
		defer closeLog()
		di.ServerAddress = s.Address
		di.DebugAddress = s.Debug
		di.Serve(ctx)
		di.MonitorMemory(ctx)
	}
	var ss jsonrpc2.StreamServer
	if s.app.Remote != "" {
		network, addr := parseAddr(s.app.Remote)
		ss = lsprpc.NewForwarder(network, addr,
			lsprpc.RemoteDebugAddress(s.RemoteDebug),
			lsprpc.RemoteListenTimeout(s.RemoteListenTimeout),
			lsprpc.RemoteLogfile(s.RemoteLogfile),
		)
	} else {
		ss = lsprpc.NewStreamServer(cache.New(ctx, s.app.options))
	}

	if s.Address != "" {
		network, addr := parseAddr(s.Address)
		return jsonrpc2.ListenAndServe(ctx, network, addr, ss, s.IdleTimeout)
	}
	if s.Port != 0 {
		addr := fmt.Sprintf(":%v", s.Port)
		return jsonrpc2.ListenAndServe(ctx, "tcp", addr, ss, s.IdleTimeout)
	}
	stream := jsonrpc2.NewHeaderStream(os.Stdin, os.Stdout)
	if s.Trace && di != nil {
		stream = protocol.LoggingStream(stream, di.LogWriter)
	}
	return ss.ServeStream(ctx, stream)
}

// parseAddr parses the -listen flag in to a network, and address.
func parseAddr(listen string) (network string, address string) {
	// Allow passing just -remote=auto, as a shorthand for using automatic remote
	// resolution.
	if listen == lsprpc.AutoNetwork {
		return lsprpc.AutoNetwork, ""
	}
	if parts := strings.SplitN(listen, ";", 2); len(parts) == 2 {
		return parts[0], parts[1]
	}
	return "tcp", listen
}
