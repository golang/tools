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
	"golang.org/x/tools/gopls/internal/util/slices"
)

// check implements the check verb for gopls.
type check struct {
	app *Application
}

func (c *check) Name() string      { return "check" }
func (c *check) Parent() string    { return c.app.Name() }
func (c *check) Usage() string     { return "<filename>" }
func (c *check) ShortHelp() string { return "show diagnostic results for the specified file" }
func (c *check) DetailedHelp(f *flag.FlagSet) {
	fmt.Fprint(f.Output(), `
Example: show the diagnostic results of this file:

	$ gopls check internal/cmd/check.go
`)
	printFlagDefaults(f)
}

// Run performs the check on the files specified by args and prints the
// results to stdout.
func (c *check) Run(ctx context.Context, args ...string) error {
	if len(args) == 0 {
		return nil
	}

	// TODO(adonovan): formally, we are required to set this
	// option if we want RelatedInformation, but it appears to
	// have no effect on the server, even though the default is
	// false. Investigate.
	origOptions := c.app.options
	c.app.options = func(opts *settings.Options) {
		if origOptions != nil {
			origOptions(opts)
		}
		opts.RelatedInformationSupported = true
	}

	conn, err := c.app.connect(ctx)
	if err != nil {
		return err
	}
	defer conn.terminate(ctx)

	// Open and diagnose the requested files.
	var (
		uris     []protocol.DocumentURI
		checking = make(map[protocol.DocumentURI]*cmdFile)
	)
	for _, arg := range args {
		uri := protocol.URIFromPath(arg)
		uris = append(uris, uri)
		file, err := conn.openFile(ctx, uri)
		if err != nil {
			return err
		}
		checking[uri] = file
	}
	if err := conn.diagnoseFiles(ctx, uris); err != nil {
		return err
	}

	// print prints a single element of a diagnostic.
	print := func(uri protocol.DocumentURI, rng protocol.Range, message string) error {
		file, err := conn.openFile(ctx, uri)
		if err != nil {
			return err
		}
		spn, err := file.rangeSpan(rng)
		if err != nil {
			return fmt.Errorf("could not convert position %v for %q", rng, message)
		}
		fmt.Printf("%v: %v\n", spn, message)
		return nil
	}

	for _, file := range checking {
		file.diagnosticsMu.Lock()
		diags := slices.Clone(file.diagnostics)
		file.diagnosticsMu.Unlock()

		for _, diag := range diags {
			if err := print(file.uri, diag.Range, diag.Message); err != nil {
				return err
			}
			for _, rel := range diag.RelatedInformation {
				if err := print(rel.Location.URI, rel.Location.Range, "- "+rel.Message); err != nil {
					return err
				}
			}

		}
	}
	return nil
}
