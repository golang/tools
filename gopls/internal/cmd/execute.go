// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package cmd

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"

	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/gopls/internal/protocol/command"
	"golang.org/x/tools/gopls/internal/server"
	"golang.org/x/tools/gopls/internal/util/slices"
	"golang.org/x/tools/internal/tool"
)

// execute implements the LSP ExecuteCommand verb for gopls.
type execute struct {
	EditFlags
	app *Application
}

func (e *execute) Name() string      { return "execute" }
func (e *execute) Parent() string    { return e.app.Name() }
func (e *execute) Usage() string     { return "[flags] command argument..." }
func (e *execute) ShortHelp() string { return "Execute a gopls custom LSP command" }
func (e *execute) DetailedHelp(f *flag.FlagSet) {
	fmt.Fprint(f.Output(), `
The execute command sends an LSP ExecuteCommand request to gopls,
with a set of optional JSON argument values.
Some commands return a result, also JSON.

Available commands are documented at:

	https://github.com/golang/tools/blob/master/gopls/doc/commands.md

This interface is experimental and commands may change or disappear without notice.

Examples:

	$ gopls execute gopls.add_import '{"ImportPath": "fmt", "URI": "file:///hello.go"}'
	$ gopls execute gopls.run_tests '{"URI": "file:///a_test.go", "Tests": ["Test"]}'
	$ gopls execute gopls.list_known_packages '{"URI": "file:///hello.go"}'

execute-flags:
`)
	printFlagDefaults(f)
}

func (e *execute) Run(ctx context.Context, args ...string) error {
	if len(args) == 0 {
		return tool.CommandLineErrorf("execute requires a command name")
	}
	cmd := args[0]
	if !slices.Contains(command.Commands, command.Command(strings.TrimPrefix(cmd, "gopls."))) {
		return tool.CommandLineErrorf("unrecognized command: %s", cmd)
	}

	// A command may have multiple arguments, though the only one
	// that currently does so is the "legacy" gopls.test,
	// so we don't show an example of it.
	var jsonArgs []json.RawMessage
	for i, arg := range args[1:] {
		var dummy any
		if err := json.Unmarshal([]byte(arg), &dummy); err != nil {
			return fmt.Errorf("argument %d is not valid JSON: %v", i+1, err)
		}
		jsonArgs = append(jsonArgs, json.RawMessage(arg))
	}

	e.app.editFlags = &e.EditFlags // in case command performs an edit

	cmdDone, onProgress := commandProgress()
	conn, err := e.app.connect(ctx, onProgress)
	if err != nil {
		return err
	}
	defer conn.terminate(ctx)

	res, err := conn.executeCommand(ctx, cmdDone, &protocol.Command{
		Command:   cmd,
		Arguments: jsonArgs,
	})
	if err != nil {
		return err
	}
	if res != nil {
		data, err := json.MarshalIndent(res, "", "\t")
		if err != nil {
			log.Fatal(err)
		}
		fmt.Printf("%s\n", data)
	}
	return nil
}

// -- shared command helpers --

const cmdProgressToken = "cmd-progress"

// TODO(adonovan): disentangle this from app.connect, and factor with
// conn.executeCommand used by codelens and execute. Seems like
// connection needs a way to register and unregister independent
// handlers, later than at connect time.
func commandProgress() (<-chan bool, func(p *protocol.ProgressParams)) {
	cmdDone := make(chan bool, 1)
	onProgress := func(p *protocol.ProgressParams) {
		switch v := p.Value.(type) {
		case *protocol.WorkDoneProgressReport:
			// TODO(adonovan): how can we segregate command's stdout and
			// stderr so that structure is preserved?
			fmt.Fprintln(os.Stderr, v.Message)

		case *protocol.WorkDoneProgressEnd:
			if p.Token == cmdProgressToken {
				// commandHandler.run sends message = canceled | failed | completed
				cmdDone <- v.Message == server.CommandCompleted
			}
		}
	}
	return cmdDone, onProgress
}

func (conn *connection) executeCommand(ctx context.Context, done <-chan bool, cmd *protocol.Command) (any, error) {
	res, err := conn.ExecuteCommand(ctx, &protocol.ExecuteCommandParams{
		Command:   cmd.Command,
		Arguments: cmd.Arguments,
		WorkDoneProgressParams: protocol.WorkDoneProgressParams{
			WorkDoneToken: cmdProgressToken,
		},
	})
	if err != nil {
		return nil, err
	}

	// Wait for it to finish (by watching for a progress token).
	//
	// In theory this is only necessary for the two async
	// commands (RunGovulncheck and RunTests), but the tests
	// fail for Test as well (why?), and there is no cost to
	// waiting in all cases. TODO(adonovan): investigate.
	if success := <-done; !success {
		// TODO(adonovan): suppress this message;
		// the command's stderr should suffice.
		return nil, fmt.Errorf("command failed")
	}

	return res, nil
}
