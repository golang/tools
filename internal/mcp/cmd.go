// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package mcp

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"syscall"
	"time"
)

// A CommandTransport is a [Transport] that runs a command and communicates
// with it over stdin/stdout, using newline-delimited JSON.
type CommandTransport struct {
	cmd *exec.Cmd
}

// NewCommandTransport returns a [CommandTransport] that runs the given command
// and communicates with it over stdin/stdout.
//
// The resulting transport takes ownership of the command, starting it during
// [CommandTransport.Connect], and stopping it when the connection is closed.
func NewCommandTransport(cmd *exec.Cmd) *CommandTransport {
	return &CommandTransport{cmd}
}

// Connect starts the command, and connects to it over stdin/stdout.
func (t *CommandTransport) Connect(ctx context.Context) (Stream, error) {
	stdout, err := t.cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stdout = io.NopCloser(stdout) // close the connection by closing stdin, not stdout
	stdin, err := t.cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	if err := t.cmd.Start(); err != nil {
		return nil, err
	}
	return newIOStream(&pipeRWC{t.cmd, stdout, stdin}), nil
}

// A pipeRWC is an io.ReadWriteCloser that communicates with a subprocess over
// stdin/stdout pipes.
type pipeRWC struct {
	cmd    *exec.Cmd
	stdout io.ReadCloser
	stdin  io.WriteCloser
}

func (s *pipeRWC) Read(p []byte) (n int, err error) {
	return s.stdout.Read(p)
}

func (s *pipeRWC) Write(p []byte) (n int, err error) {
	return s.stdin.Write(p)
}

// Close closes the input stream to the child process, and awaits normal
// termination of the command. If the command does not exit, it is signalled to
// terminate, and then eventually killed.
func (s *pipeRWC) Close() error {
	// Spec:
	// "For the stdio transport, the client SHOULD initiate shutdown by:...

	// "...First, closing the input stream to the child process (the server)"
	if err := s.stdin.Close(); err != nil {
		return fmt.Errorf("closing stdin: %v", err)
	}
	resChan := make(chan error, 1)
	go func() {
		resChan <- s.cmd.Wait()
	}()
	// "...Waiting for the server to exit, or sending SIGTERM if the server does not exit within a reasonable time"
	wait := func() (error, bool) {
		select {
		case err := <-resChan:
			return err, true
		case <-time.After(5 * time.Second):
		}
		return nil, false
	}
	if err, ok := wait(); ok {
		return err
	}
	// Note the condition here: if sending SIGTERM fails, don't wait and just
	// move on to SIGKILL.
	if err := s.cmd.Process.Signal(syscall.SIGTERM); err == nil {
		if err, ok := wait(); ok {
			return err
		}
	}
	// "...Sending SIGKILL if the server does not exit within a reasonable time after SIGTERM"
	if err := s.cmd.Process.Kill(); err != nil {
		return err
	}
	if err, ok := wait(); ok {
		return err
	}
	return fmt.Errorf("unresponsive subprocess")
}
