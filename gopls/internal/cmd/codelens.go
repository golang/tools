// Copyright 2019 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package cmd

import (
	"context"
	"flag"
	"fmt"

	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/gopls/internal/settings"
	"golang.org/x/tools/internal/tool"
)

// codelens implements the codelens verb for gopls.
type codelens struct {
	EditFlags
	app *Application

	Exec bool `flag:"exec" help:"execute the first matching code lens"`
}

func (r *codelens) Name() string      { return "codelens" }
func (r *codelens) Parent() string    { return r.app.Name() }
func (r *codelens) Usage() string     { return "[codelens-flags] file[:line[:col]] [title]" }
func (r *codelens) ShortHelp() string { return "List or execute code lenses for a file" }
func (r *codelens) DetailedHelp(f *flag.FlagSet) {
	fmt.Fprint(f.Output(), `
The codelens command lists or executes code lenses for the specified
file, or line within a file. A code lens is a command associated with
a position in the code.

With an optional title argment, only code lenses matching that
title are considered.

By default, the codelens command lists the available lenses for the
specified file or line within a file, including the title and
title of the command. With the -exec flag, the first matching command
is executed, and its output is printed to stdout.

Example:

	$ gopls codelens a_test.go                    # list code lenses in a file
	$ gopls codelens a_test.go:10                 # list code lenses on line 10
	$ gopls codelens a_test.go gopls.test         # list gopls.test commands
	$ gopls codelens -run a_test.go:10 gopls.test # run a specific test

codelens-flags:
`)
	printFlagDefaults(f)
}

func (r *codelens) Run(ctx context.Context, args ...string) error {
	var filename, title string
	switch len(args) {
	case 0:
		return tool.CommandLineErrorf("codelens requires a file name")
	case 2:
		title = args[1]
		fallthrough
	case 1:
		filename = args[0]
	default:
		return tool.CommandLineErrorf("codelens expects at most two arguments")
	}

	r.app.editFlags = &r.EditFlags // in case a codelens perform an edit

	// Override the default setting for codelenses["test"], which is
	// off by default because VS Code has a superior client-side
	// implementation. But this client is not VS Code.
	// See golang.LensFuncs().
	origOptions := r.app.options
	r.app.options = func(opts *settings.Options) {
		if origOptions != nil {
			origOptions(opts)
		}
		if opts.Codelenses == nil {
			opts.Codelenses = make(map[settings.CodeLensSource]bool)
		}
		opts.Codelenses[settings.CodeLensTest] = true
	}

	conn, err := r.app.connect(ctx)
	if err != nil {
		return err
	}
	defer conn.terminate(ctx)

	filespan := parseSpan(filename)
	file, err := conn.openFile(ctx, filespan.URI())
	if err != nil {
		return err
	}
	loc, err := file.spanLocation(filespan)
	if err != nil {
		return err
	}

	p := protocol.CodeLensParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: loc.URI},
	}
	lenses, err := conn.CodeLens(ctx, &p)
	if err != nil {
		return err
	}

	for _, lens := range lenses {
		sp, err := file.rangeSpan(lens.Range)
		if err != nil {
			return nil
		}

		if title != "" && lens.Command.Title != title {
			continue // title was specified but does not match
		}
		if filespan.HasPosition() && !protocol.Intersect(loc.Range, lens.Range) {
			continue // position was specified but does not match
		}

		// -exec: run the first matching code lens.
		if r.Exec {
			_, err := conn.executeCommand(ctx, lens.Command)
			return err
		}

		// No -exec: list matching code lenses.
		fmt.Printf("%v: %q [%s]\n", sp, lens.Command.Title, lens.Command.Command)
	}

	if r.Exec {
		return fmt.Errorf("no code lens at %s with title %q", filespan, title)
	}
	return nil
}
