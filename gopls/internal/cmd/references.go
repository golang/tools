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
	"slices"
	"strings"

	"golang.org/x/tools/gopls/internal/protocol"
)

// referencesJSON is the result of a 'references' query.
type referencesJSON struct {
	References []referenceJSON `json:"references"`
}

type referenceJSON struct {
	Location span   `json:"location"`
	Context  string `json:"context,omitempty"` // Surrounding code context (present if -context > 0)
}

// references implements the references verb for gopls
type references struct {
	TargetQueryFlags
	app *application

	IncludeDeclaration bool `flag:"d,declaration" help:"include the declaration of the specified identifier in the results"`
	Context            int  `flag:"context" help:"number of lines of surrounding code context to print around each reference (inclusive of ref line, so 1 prints just the ref line)"`
	Limit              int  `flag:"limit" help:"maximum number of results to return (0 means no limit)"`
	JSON               bool `flag:"json" help:"emit output in JSON format"`
}

func (r *references) Name() string      { return "references" }
func (r *references) Parent() string    { return r.app.Name() }
func (r *references) Usage() string     { return "[references-flags] <target>" }
func (r *references) ShortHelp() string { return "display selected identifier's references" }
func (r *references) DetailedHelp(f *flag.FlagSet) {
	fmt.Fprint(f.Output(), `
Example:
	$ gopls references -pkg go/ast Decl
	$ gopls references golang.org/x/tools/go/ast/inspector.Cursor

references-flags:
`)
	printFlagDefaults(f)
}

func (r *references) Run(ctx context.Context, args ...string) error {
	if len(args) != 1 {
		return commandLineErrorf("references expects 1 argument (target)")
	}
	if r.Context < 0 {
		return commandLineErrorf("-context must not be negative")
	}
	if r.Limit < 0 {
		return commandLineErrorf("-limit must not be negative")
	}

	cli, _, err := r.app.connect(ctx)
	if err != nil {
		return err
	}
	defer cli.terminate(ctx)

	root, err := os.Getwd() // resolve relative to the user's current dir
	if err != nil {
		return fmt.Errorf("finding workdir: %v", err)
	}
	target, err := resolveSingleTarget(ctx, cli, r.TargetQueryFlags, root, args[0])
	if err != nil {
		return err
	}

	p := protocol.ReferenceParams{
		Context: protocol.ReferenceContext{
			IncludeDeclaration: r.IncludeDeclaration,
		},
		TextDocumentPositionParams: protocol.LocationTextDocumentPositionParams(target.loc),
	}
	locations, err := cli.server.References(ctx, &p)
	if err != nil {
		return err
	}
	refs := make([]referenceJSON, 0, len(locations))
	for _, l := range locations {
		f, err := cli.openFile(ctx, l.URI)
		if err != nil {
			return err
		}
		// convert location to span for user-friendly 1-indexed line
		// and column numbers
		s, err := f.locationSpan(l)
		if err != nil {
			return err
		}
		var refContext string
		if r.Context > 0 {
			refContext = extractContext(f.mapper.Content, s, r.Context)
		}
		refs = append(refs, referenceJSON{
			Location: s,
			Context:  refContext,
		})
	}

	slices.SortFunc(refs, func(a, b referenceJSON) int {
		return compare(a.Location, b.Location)
	})
	if r.Limit > 0 && len(refs) > r.Limit {
		refs = refs[:r.Limit]
	}

	result := &referencesJSON{
		References: refs,
	}
	if r.JSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "\t")
		return enc.Encode(result)
	}
	for _, ref := range result.References {
		fmt.Println(ref.Location)
		if r.Context > 0 && ref.Context != "" {
			startLine := max(1, ref.Location.Start().Line()-(r.Context-1))
			for i, line := range strings.Split(ref.Context, "\n") {
				fmt.Printf("\t%d: %s\n", startLine+i, line)
			}
			fmt.Print("\n")
		}
	}
	return nil
}

// extractContext returns the lines of content surrounding the span s,
// including contextLines-1 lines before the start line and after the end line
// (so contextLines == 1 returns only the lines covered by s).
func extractContext(content []byte, s span, contextLines int) string {
	lines := strings.Split(strings.TrimSuffix(string(content), "\n"), "\n")
	for i := range lines {
		lines[i] = strings.TrimSuffix(lines[i], "\r")
	}
	padding := contextLines - 1
	startLine := max(1, s.Start().Line()-padding)
	endLine := min(len(lines), s.End().Line()+padding)
	if startLine > endLine || startLine > len(lines) {
		return ""
	}
	return strings.Join(lines[startLine-1:endLine], "\n")
}
