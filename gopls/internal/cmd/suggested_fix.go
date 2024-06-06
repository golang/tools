// Copyright 2019 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package cmd

import (
	"context"
	"flag"
	"fmt"

	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/gopls/internal/util/slices"
	"golang.org/x/tools/internal/tool"
)

// TODO(adonovan): this command has a very poor user interface. It
// should have a way to query the available fixes for a file (without
// a span), enumerate the valid fix kinds, enable all fixes, and not
// require the pointless -all flag. See issue #60290.

// suggestedFix implements the fix verb for gopls.
type suggestedFix struct {
	EditFlags
	All bool `flag:"a,all" help:"apply all fixes, not just preferred fixes"`

	app *Application
}

func (s *suggestedFix) Name() string      { return "fix" }
func (s *suggestedFix) Parent() string    { return s.app.Name() }
func (s *suggestedFix) Usage() string     { return "[fix-flags] <filename>" }
func (s *suggestedFix) ShortHelp() string { return "apply suggested fixes" }
func (s *suggestedFix) DetailedHelp(f *flag.FlagSet) {
	fmt.Fprintf(f.Output(), `
Example: apply fixes to this file, rewriting it:

	$ gopls fix -a -w internal/cmd/check.go

The -a (-all) flag causes all fixes, not just preferred ones, to be
applied, but since no fixes are currently preferred, this flag is
essentially mandatory.

Arguments after the filename are interpreted as LSP CodeAction kinds
to be applied; the default set is {"quickfix"}, but valid kinds include:

	quickfix
	refactor
	refactor.extract
	refactor.inline
	refactor.rewrite
	source.organizeImports
	source.fixAll

CodeAction kinds are hierarchical, so "refactor" includes
"refactor.inline". There is currently no way to enable or even
enumerate all kinds.

Example: apply any "refactor.rewrite" fixes at the specific byte
offset within this file:

	$ gopls fix -a internal/cmd/check.go:#43 refactor.rewrite

fix-flags:
`)
	printFlagDefaults(f)
}

// Run performs diagnostic checks on the file specified and either;
// - if -w is specified, updates the file in place;
// - if -d is specified, prints out unified diffs of the changes; or
// - otherwise, prints the new versions to stdout.
func (s *suggestedFix) Run(ctx context.Context, args ...string) error {
	if len(args) < 1 {
		return tool.CommandLineErrorf("fix expects at least 1 argument")
	}
	s.app.editFlags = &s.EditFlags
	conn, err := s.app.connect(ctx)
	if err != nil {
		return err
	}
	defer conn.terminate(ctx)

	from := parseSpan(args[0])
	uri := from.URI()
	file, err := conn.openFile(ctx, uri)
	if err != nil {
		return err
	}
	rng, err := file.spanRange(from)
	if err != nil {
		return err
	}

	// Get diagnostics.
	if err := conn.diagnoseFiles(ctx, []protocol.DocumentURI{uri}); err != nil {
		return err
	}
	diagnostics := []protocol.Diagnostic{} // LSP wants non-nil slice
	conn.client.filesMu.Lock()
	diagnostics = append(diagnostics, file.diagnostics...)
	conn.client.filesMu.Unlock()

	// Request code actions
	codeActionKinds := []protocol.CodeActionKind{protocol.QuickFix}
	if len(args) > 1 {
		codeActionKinds = []protocol.CodeActionKind{}
		for _, k := range args[1:] {
			codeActionKinds = append(codeActionKinds, protocol.CodeActionKind(k))
		}
	}
	p := protocol.CodeActionParams{
		TextDocument: protocol.TextDocumentIdentifier{
			URI: uri,
		},
		Context: protocol.CodeActionContext{
			Only:        codeActionKinds,
			Diagnostics: diagnostics,
		},
		Range: rng,
	}
	actions, err := conn.CodeAction(ctx, &p)
	if err != nil {
		return fmt.Errorf("%v: %v", from, err)
	}

	// Gather edits from matching code actions.
	var edits []protocol.TextEdit
	for _, a := range actions {
		// Without -all, apply only "preferred" fixes.
		if !a.IsPreferred && !s.All {
			continue
		}

		// Execute any command.
		// This may cause the server to make
		// an ApplyEdit downcall to the client.
		if a.Command != nil {
			if _, err := conn.executeCommand(ctx, a.Command); err != nil {
				return err
			}
			// The specification says that commands should
			// be executed _after_ edits are applied, not
			// instead of them, but we don't want to
			// duplicate edits.
			continue
		}

		// If the provided span has a position (not just offsets),
		// and the action has diagnostics, the action must have a
		// diagnostic with the same range as it.
		if from.HasPosition() && len(a.Diagnostics) > 0 &&
			!slices.ContainsFunc(a.Diagnostics, func(diag protocol.Diagnostic) bool {
				return diag.Range.Start == rng.Start
			}) {
			continue
		}

		// Partially apply CodeAction.Edit, a WorkspaceEdit.
		// (See also conn.Client.applyWorkspaceEdit(a.Edit)).
		for _, c := range a.Edit.DocumentChanges {
			tde := c.TextDocumentEdit
			if tde != nil && tde.TextDocument.URI == uri {
				// TODO(adonovan): this logic will butcher an edit that spans files.
				// It will also ignore create/delete/rename operations.
				// Fix or document.
				edits = append(edits, protocol.AsTextEdits(tde.Edits)...)
			}
		}
	}

	return applyTextEdits(file.mapper, edits, s.app.editFlags)
}
