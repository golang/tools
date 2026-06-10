// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package cmd

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/tools/gopls/internal/tool"
)

// Main is the main entrypoint for the gopls application, called by gopls.main.
// It initializes a fresh [Application] via [New] and coordinates flag dispatch
// and execution.
func Main(ctx context.Context, args []string) {
	app := New()
	subApp, restArgs, p, err := dispatch(app, args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "gopls: %v\n", err)
		if tool.IsCommandLineError(err) {
			fs := flag.NewFlagSet("gopls", flag.ContinueOnError)
			fs.SetOutput(os.Stderr)
			app.DetailedHelp(fs)
		}
		os.Exit(2)
	}

	if err := tool.RunWithProfile(ctx, subApp, restArgs, p); err != nil {
		fmt.Fprintf(os.Stderr, "gopls: %v\n", err)
		if tool.IsCommandLineError(err) {
			fs := flag.NewFlagSet(subApp.Name(), flag.ContinueOnError)
			fs.SetOutput(os.Stderr)
			fmt.Fprintf(os.Stderr, "%s [global flags] %s [flags]\n", app.Name(), subApp.Name())
			subApp.DetailedHelp(fs)
		}
		os.Exit(2)
	}
}

// dispatch parses command line arguments using a two-phase FlagSet algorithm:
//
// # Phase 1 (Global & Pre-subcommand Flags):
//
// This phase parses all initial arguments up to the first subcommand
// (or non-flag argument) using a combined FlagSet that contains
// both global application flags and serve flags
// (to maintain backward compatibility with legacy serve invocations).
//
// # Phase 2 (Subcommand Flags):
//
// This phase determines the target subcommand from the remaining arguments
// and parses subsequent flags using a specific FlagSet for that subcommand.
// Because Phase 1 stops exactly at the subcommand name if any,
// Phase 1 and Phase 2 never parse the same argument twice
// (ensuring slice/repeatable flags accumulate correctly).
//
// [dispatch] enforces strict POSIX flag ordering for non-serve subcommands,
// and returns the specific [tool.Application] to execute,
// its positional arguments, and
// any core [tool.Profile] configuration discovered during global flag parsing.
func dispatch(app *Application, args []string) (tool.Application, []string, *tool.Profile, error) {
	silentFlagSet := func(name string) *flag.FlagSet {
		fs := flag.NewFlagSet(name, flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		return fs
	}

	globalFS := silentFlagSet(app.Name())
	tool.AddFlags(globalFS, app)
	p := &app.Profile

	// Phase 1: Global and implicit serve flag parsing.
	combinedFS := silentFlagSet(app.Name())
	tool.AddFlags(combinedFS, app)
	tool.AddFlags(combinedFS, &app.serve)

	if err := combinedFS.Parse(args); err != nil {
		return nil, nil, nil, err
	}

	rest := combinedFS.Args()
	if len(rest) == 0 { // Implicit serve.
		return &app.serve, nil, p, nil
	}

	cmdName := rest[0]

	if cmdName == "help" {
		helpApp := &help{app: app}
		return helpApp, rest[1:], p, nil
	}

	if cmdName == "serve" {
		// Legacy compatibility: allow serve flags before or after 'serve'.
		serveFS := silentFlagSet("serve")
		tool.AddFlags(serveFS, &app.serve)
		if err := serveFS.Parse(rest[1:]); err != nil {
			return nil, nil, nil, enhanceFlagParseError(err, globalFS, "serve")
		}
		return &app.serve, serveFS.Args(), p, nil
	}

	var subApp tool.Application
	for _, cmd := range app.Commands() {
		if cmd.Name() == cmdName {
			subApp = cmd
			break
		}
	}
	if subApp == nil {
		// Fallback: assume implicit 'serve' with positional arguments
		// (which will fail in Serve.Run, matching legacy NormalizeArgs behavior).
		return &app.serve, rest, p, nil
	}

	// Phase 2: Subcommand flag parsing - except "serve".
	// Enforce POSIX ordering: verify no subcommand flags appeared before the subcommand name.
	var err error
	combinedFS.Visit(func(f *flag.Flag) {
		if globalFS.Lookup(f.Name) == nil {
			err = tool.CommandLineErrorf("flag -%s belongs to subcommand but is placed before it", f.Name)
		}
	})
	if err != nil {
		return nil, nil, nil, err
	}

	subFS := silentFlagSet(cmdName)
	tool.AddFlags(subFS, subApp)
	if err := subFS.Parse(rest[1:]); err != nil {
		return nil, nil, nil, enhanceFlagParseError(err, globalFS, cmdName)
	}
	return subApp, subFS.Args(), p, nil
}

// enhanceFlagParseError returns an enhanced flag parsing error that is more specific
// when a flag is placed before a subcommand.
func enhanceFlagParseError(err error, globals *flag.FlagSet, cmdName string) error {
	if after, ok := strings.CutPrefix(err.Error(), "flag provided but not defined: "); ok {
		fname := strings.TrimPrefix(after, "-")
		if globals.Lookup(fname) != nil {
			return tool.CommandLineErrorf("global flag -%s must be placed before subcommand %s", fname, cmdName)
		}
		return tool.CommandLineErrorf("unknown flag: -%s", fname)
	}
	return err
}
