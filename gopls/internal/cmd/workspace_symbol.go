// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package cmd

import (
	"context"
	"flag"
	"fmt"
	"strings"

	"golang.org/x/tools/gopls/internal/lsp/protocol"
	"golang.org/x/tools/gopls/internal/settings"
	"golang.org/x/tools/internal/tool"
)

// workspaceSymbol implements the workspace_symbol verb for gopls.
type workspaceSymbol struct {
	Matcher string `flag:"matcher" help:"specifies the type of matcher: fuzzy, fastfuzzy, casesensitive, or caseinsensitive.\nThe default is caseinsensitive."`

	app *Application
}

func (r *workspaceSymbol) Name() string      { return "workspace_symbol" }
func (r *workspaceSymbol) Parent() string    { return r.app.Name() }
func (r *workspaceSymbol) Usage() string     { return "[workspace_symbol-flags] <query>" }
func (r *workspaceSymbol) ShortHelp() string { return "search symbols in workspace" }
func (r *workspaceSymbol) DetailedHelp(f *flag.FlagSet) {
	fmt.Fprint(f.Output(), `
Example:

	$ gopls workspace_symbol -matcher fuzzy 'wsymbols'

workspace_symbol-flags:
`)
	printFlagDefaults(f)
}

func (r *workspaceSymbol) Run(ctx context.Context, args ...string) error {
	if len(args) != 1 {
		return tool.CommandLineErrorf("workspace_symbol expects 1 argument")
	}

	opts := r.app.options
	r.app.options = func(o *settings.Options) {
		if opts != nil {
			opts(o)
		}
		switch strings.ToLower(r.Matcher) {
		case "fuzzy":
			o.SymbolMatcher = settings.SymbolFuzzy
		case "casesensitive":
			o.SymbolMatcher = settings.SymbolCaseSensitive
		case "fastfuzzy":
			o.SymbolMatcher = settings.SymbolFastFuzzy
		default:
			o.SymbolMatcher = settings.SymbolCaseInsensitive
		}
	}

	conn, err := r.app.connect(ctx, nil)
	if err != nil {
		return err
	}
	defer conn.terminate(ctx)

	p := protocol.WorkspaceSymbolParams{
		Query: args[0],
	}

	symbols, err := conn.Symbol(ctx, &p)
	if err != nil {
		return err
	}
	for _, s := range symbols {
		f, err := conn.openFile(ctx, s.Location.URI)
		if err != nil {
			return err
		}
		span, err := f.locationSpan(s.Location)
		if err != nil {
			return err
		}
		fmt.Printf("%s %s %s\n", span, s.Name, s.Kind)
	}

	return nil
}
