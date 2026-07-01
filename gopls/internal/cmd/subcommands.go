// Copyright 2021 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package cmd

import (
	"context"
	"flag"
	"fmt"
	"text/tabwriter"
)

// subcommands is a helper that may be embedded for commands that delegate to
// subcommands.
type subcommands []command

func (s subcommands) DetailedHelp(f *flag.FlagSet) {
	w := tabwriter.NewWriter(f.Output(), 0, 0, 2, ' ', 0)
	defer w.Flush()
	fmt.Fprint(w, "\nSubcommand:\n")
	for _, c := range s {
		fmt.Fprintf(w, "  %s\t%s\n", c.Name(), c.ShortHelp())
	}
	printFlagDefaults(f)
}

func (s subcommands) Usage() string { return "<subcommand> [arg]..." }

func (s subcommands) Run(ctx context.Context, args ...string) error {
	if len(args) == 0 {
		return commandLineErrorf("must provide subcommand")
	}
	command, args := args[0], args[1:]
	for _, c := range s {
		if c.Name() == command {
			fs := parseFlags(c, args)
			return c.Run(ctx, fs.Args()...)
		}
	}
	return commandLineErrorf("unknown subcommand %v", command)
}

func (s subcommands) Commands() []command { return s }

// getSubcommands returns the subcommands of a given command.
func getSubcommands(a command) []command {
	// This interface is satisfied both by commands
	// that embed subcommands, and by *cmd.application.
	type hasCommands interface {
		Commands() []command
	}
	if sub, ok := a.(hasCommands); ok {
		return sub.Commands()
	}
	return nil
}
