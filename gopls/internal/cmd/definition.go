// Copyright 2019 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package cmd

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/gopls/internal/settings"
	"golang.org/x/tools/internal/tool"
)

// A Definition is the result of a 'definition' query.
type Definition struct {
	Span        span   `json:"span"`        // span of the definition
	Description string `json:"description"` // description of the denoted object
}

// These constant is printed in the help, and then used in a test to verify the
// help is still valid.
// They refer to "Set" in "flag.FlagSet" from the DetailedHelp method below.
const (
	exampleLine   = 44
	exampleColumn = 47
	exampleOffset = 1270
)

// definition implements the definition verb for gopls.
type definition struct {
	app *Application

	JSON              bool `flag:"json" help:"emit output in JSON format"`
	MarkdownSupported bool `flag:"markdown" help:"support markdown in responses"`
}

func (d *definition) Name() string      { return "definition" }
func (d *definition) Parent() string    { return d.app.Name() }
func (d *definition) Usage() string     { return "[definition-flags] <position>" }
func (d *definition) ShortHelp() string { return "show declaration of selected identifier" }
func (d *definition) DetailedHelp(f *flag.FlagSet) {
	fmt.Fprintf(f.Output(), `
Example: show the definition of the identifier at syntax at offset %[1]v in this file (flag.FlagSet):

	$ gopls definition internal/cmd/definition.go:%[1]v:%[2]v
	$ gopls definition internal/cmd/definition.go:#%[3]v

definition-flags:
`, exampleLine, exampleColumn, exampleOffset)
	printFlagDefaults(f)
}

// Run performs the definition query as specified by args and prints the
// results to stdout.
func (d *definition) Run(ctx context.Context, args ...string) error {
	if len(args) != 1 {
		return tool.CommandLineErrorf("definition expects 1 argument")
	}
	// Plaintext makes more sense for the command line.
	opts := d.app.options
	d.app.options = func(o *settings.Options) {
		if opts != nil {
			opts(o)
		}
		o.PreferredContentFormat = protocol.PlainText
		if d.MarkdownSupported {
			o.PreferredContentFormat = protocol.Markdown
		}
	}
	conn, err := d.app.connect(ctx)
	if err != nil {
		return err
	}
	defer conn.terminate(ctx)
	from := parseSpan(args[0])
	file, err := conn.openFile(ctx, from.URI())
	if err != nil {
		return err
	}
	loc, err := file.spanLocation(from)
	if err != nil {
		return err
	}
	p := protocol.DefinitionParams{
		TextDocumentPositionParams: protocol.LocationTextDocumentPositionParams(loc),
	}
	locs, err := conn.Definition(ctx, &p)
	if err != nil {
		return fmt.Errorf("%v: %v", from, err)
	}

	if len(locs) == 0 {
		return fmt.Errorf("%v: not an identifier", from)
	}
	file, err = conn.openFile(ctx, locs[0].URI)
	if err != nil {
		return fmt.Errorf("%v: %v", from, err)
	}
	definition, err := file.locationSpan(locs[0])
	if err != nil {
		return fmt.Errorf("%v: %v", from, err)
	}

	q := protocol.HoverParams{
		TextDocumentPositionParams: protocol.LocationTextDocumentPositionParams(loc),
	}
	hover, err := conn.Hover(ctx, &q)
	if err != nil {
		return fmt.Errorf("%v: %v", from, err)
	}
	var description string
	if hover != nil {
		description = strings.TrimSpace(hover.Contents.Value)
	}

	result := &Definition{
		Span:        definition,
		Description: description,
	}
	if d.JSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "\t")
		return enc.Encode(result)
	}
	fmt.Printf("%v", result.Span)
	if len(result.Description) > 0 {
		fmt.Printf(": defined here as %s", result.Description)
	}
	fmt.Printf("\n")
	return nil
}
