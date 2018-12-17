// Copyright 2018 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package cmd

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/tools/internal/jsonrpc2"
	"golang.org/x/tools/internal/lsp"
	"golang.org/x/tools/internal/tool"
)

// server is a struct that exposes the configurable parts of the LSP server as
// flags, in the right form for tool.Main to consume.
type server struct {
	Logfile string `flag:"logfile" help:"filename to log to. if value is \"auto\", then logging to a default output file is enabled"`
	Mode    string `flag:"mode" help:"no effect"`
}

func (s *server) Name() string  { return "server" }
func (s *server) Usage() string { return "" }
func (s *server) ShortHelp() string {
	return "run a server for Go code using the Language Server Protocol"
}
func (s *server) DetailedHelp(f *flag.FlagSet) {
	fmt.Fprint(f.Output(), `
The server communicates using JSONRPC2 on stdin and stdout, and is intended to be run directly as
a child of an editor process.
`)
}

// Run configures a server based on the flags, and then runs it.
// It blocks until the server shuts down.
func (s *server) Run(ctx context.Context, args ...string) error {
	if len(args) > 0 {
		return tool.CommandLineErrorf("server does not take arguments, got %v", args)
	}
	out := os.Stderr
	if s.Logfile != "" {
		filename := s.Logfile
		if filename == "auto" {
			filename = filepath.Join(os.TempDir(), fmt.Sprintf("golsp-%d.log", os.Getpid()))
		}
		f, err := os.Create(filename)
		if err != nil {
			return fmt.Errorf("Unable to create log file: %v", err)
		}
		defer f.Close()
		log.SetOutput(io.MultiWriter(os.Stderr, f))
		out = f
	}
	return lsp.RunServer(
		ctx,
		jsonrpc2.NewHeaderStream(os.Stdin, os.Stdout),
		func(direction jsonrpc2.Direction, id *jsonrpc2.ID, elapsed time.Duration, method string, payload *json.RawMessage, err *jsonrpc2.Error) {
			const eol = "\r\n\r\n\r\n"
			if err != nil {
				fmt.Fprintf(out, "[Error - %v] %s %s%s %v%s", time.Now().Format("3:04:05 PM"),
					direction, method, id, err, eol)
				return
			}
			outx := new(strings.Builder)
			fmt.Fprintf(outx, "[Trace - %v] ", time.Now().Format("3:04:05 PM"))
			switch direction {
			case jsonrpc2.Send:
				fmt.Fprint(outx, "Received ")
			case jsonrpc2.Receive:
				fmt.Fprint(outx, "Sending ")
			}
			switch {
			case id == nil:
				fmt.Fprint(outx, "notification ")
			case elapsed >= 0:
				fmt.Fprint(outx, "response ")
			default:
				fmt.Fprint(outx, "request ")
			}
			fmt.Fprintf(outx, "'%s", method)
			switch {
			case id == nil:
				// do nothing
			case id.Name != "":
				fmt.Fprintf(outx, " - (%s)", id.Name)
			default:
				fmt.Fprintf(outx, " - (%d)", id.Number)
			}
			fmt.Fprint(outx, "'")
			if elapsed >= 0 {
				fmt.Fprintf(outx, " in %vms", elapsed.Nanoseconds()/1000)
			}
			params := string(*payload)
			if params == "null" {
				params = "{}"
			}
			fmt.Fprintf(outx, ".\r\nParams: %s%s", params, eol)
			fmt.Fprintf(out, "%s", outx.String())
		},
	)
}
