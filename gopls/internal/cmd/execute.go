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
	"slices"

	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/gopls/internal/protocol/command"
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

Gopls' command set is defined by the command.Interface type; see
https://pkg.go.dev/golang.org/x/tools/gopls/internal/protocol/command#Interface.
It is not a stable interface: commands may change or disappear without notice.

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
	if !slices.Contains(command.Commands, command.Command(cmd)) {
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

	conn, err := e.app.connect(ctx)
	if err != nil {
		return err
	}
	defer conn.terminate(ctx)

	res, err := conn.executeCommand(ctx, &protocol.Command{
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

// executeCommand executes a protocol.Command, displaying progress
// messages and awaiting completion of asynchronous commands.
//
// TODO(rfindley): inline away all calls, ensuring they inline idiomatically.
func (conn *connection) executeCommand(ctx context.Context, cmd *protocol.Command) (any, error) {
	return conn.ExecuteCommand(ctx, &protocol.ExecuteCommandParams{
		Command:   cmd.Command,
		Arguments: cmd.Arguments,
	})
}
