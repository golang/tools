// Copyright 2026 The Go Authors. All rights reserved.
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
)

// inspectJSON is the result of an 'inspect' query.
type inspectJSON struct {
	Definition span `json:"definition"`
	// TODO: structured json instead of string?
	Hover     string `json:"hover"`            // Plain text or Markdown documentation
	Signature string `json:"signature"`        // Formatted Go signature (for example, "func Printf(format string, a ...any) (n int, err error)")
	Source    string `json:"source,omitempty"` // Present if -src flag is active
}

// inspect implements the inspect verb for gopls
// TODO(aputman): Add the flags: -src, -full
type inspect struct {
	TargetQueryFlags
	app *application

	JSON     bool `flag:"json" help:"emit output in JSON format"`
	Markdown bool `flag:"markdown" help:"outputs documentation in formatted Markdown instead of plain text"`
}

func (i *inspect) Name() string   { return "inspect" }
func (i *inspect) Parent() string { return i.app.Name() }
func (i *inspect) Usage() string  { return "[inspect-flags] <target>" }
func (i *inspect) ShortHelp() string {
	return "display the target's definition location, hover docs, and signature"
}
func (i *inspect) DetailedHelp(f *flag.FlagSet) {
	fmt.Fprint(f.Output(), `
Example:
	$ gopls inspect -pkg go/ast Decl
	$ gopls inspect golang.org/x/tools/go/ast/inspector.Cursor

inspect-flags:
`)
	printFlagDefaults(f)
}
func (i *inspect) Run(ctx context.Context, args ...string) error {
	if len(args) != 1 {
		return commandLineErrorf("inspect expects 1 argument (target)")
	}
	// Plaintext makes more sense for the command line, unless the user
	// requests markdown.
	opts := i.app.options
	i.app.options = func(o *settings.Options) {
		if opts != nil {
			opts(o)
		}
		o.PreferredContentFormat = protocol.PlainText
		if i.Markdown {
			o.PreferredContentFormat = protocol.Markdown
		}
	}

	cli, _, err := i.app.connect(ctx)
	if err != nil {
		return err
	}
	defer cli.terminate(ctx)
	root, err := os.Getwd() // resolve relative to the user's current dir
	if err != nil {
		return fmt.Errorf("finding workdir: %v", err)
	}
	target, err := resolveSingleTarget(ctx, cli, i.TargetQueryFlags, root, args[0])
	if err != nil {
		return err
	}

	q := protocol.HoverParams{
		TextDocumentPositionParams: protocol.LocationTextDocumentPositionParams(target.loc),
	}
	hover, err := cli.server.Hover(ctx, &q)
	if err != nil {
		return fmt.Errorf("%s: %v", target.qualifiedName(), err)
	}
	var description string
	if hover != nil {
		description = strings.TrimSpace(hover.Contents.Value)
	}

	span, err := target.span(ctx, cli)
	if err != nil {
		return err
	}
	result := &inspectJSON{
		Definition: span,
		Hover:      description,
		// TODO: Use a custom command, instead of hover, to retrieve separated info.
		Signature: "",
		Source:    "",
	}
	if i.JSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "\t")
		return enc.Encode(result)
	}
	fmt.Printf("%v:\n", result.Definition)
	if len(result.Hover) > 0 {
		// TODO: consider adding some ansi terminal formatting
		fmt.Printf("\n%s", result.Hover)
	}
	return nil
}
