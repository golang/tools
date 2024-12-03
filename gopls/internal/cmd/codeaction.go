// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package cmd

import (
	"context"
	"flag"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/internal/tool"
)

// codeaction implements the codeaction verb for gopls.
type codeaction struct {
	EditFlags
	Kind  string `flag:"kind" help:"comma-separated list of code action kinds to filter"`
	Title string `flag:"title" help:"regular expression to match title"`
	Exec  bool   `flag:"exec" help:"execute the first matching code action"`

	app *Application
}

func (cmd *codeaction) Name() string      { return "codeaction" }
func (cmd *codeaction) Parent() string    { return cmd.app.Name() }
func (cmd *codeaction) Usage() string     { return "[codeaction-flags] filename[:line[:col]]" }
func (cmd *codeaction) ShortHelp() string { return "list or execute code actions" }
func (cmd *codeaction) DetailedHelp(f *flag.FlagSet) {
	fmt.Fprintf(f.Output(), `

The codeaction command lists or executes code actions for the
specified file or range of a file. Each code action contains
either an edit to be directly applied to the file, or a command
to be executed by the server, which may have an effect such as:
- requesting that the client apply an edit;
- changing the state of the server; or
- requesting that the client open a document.

The -kind and and -title flags filter the list of actions.

The -kind flag specifies a comma-separated list of LSP CodeAction kinds.
Only actions of these kinds will be requested from the server.
Valid kinds include:

	gopls.doc.features
	quickfix
	refactor
	refactor.extract
	refactor.extract.constant
	refactor.extract.function
	refactor.extract.method
	refactor.extract.toNewFile
	refactor.extract.variable
	refactor.inline
	refactor.inline.call
	refactor.rewrite
	refactor.rewrite.changeQuote
	refactor.rewrite.fillStruct
	refactor.rewrite.fillSwitch
	refactor.rewrite.invertIf
	refactor.rewrite.joinLines
	refactor.rewrite.removeUnusedParam
	refactor.rewrite.splitLines
	source
	source.assembly
	source.doc
	source.fixAll
	source.freesymbols
	source.organizeImports
	source.test

Kinds are hierarchical, so "refactor" includes "refactor.inline".
(Note: actions of kind "source.test" are not returned unless explicitly
requested.)

The -title flag specifies a regular expression that must match the
action's title. (Ideally kinds would be specific enough that this
isn't necessary; we really need to subdivide refactor.rewrite; see
gopls/internal/settings/codeactionkind.go.)

The -exec flag causes the first matching code action to be executed.
Without the flag, the matching actions are merely listed.

It is not currently possible to execute more than one action,
as that requires a way to detect and resolve conflicts.
TODO(adonovan): support it when golang/go#67049 is resolved.

If executing an action causes the server to send a patch to the
client, the usual -write, -preserve, -diff, and -list flags govern how
the client deals with the patch.

Example: execute the first "quick fix" in the specified file and show the diff:

	$ gopls codeaction -kind=quickfix -exec -diff ./gopls/main.go

codeaction-flags:
`)
	printFlagDefaults(f)
}

func (cmd *codeaction) Run(ctx context.Context, args ...string) error {
	if len(args) < 1 {
		return tool.CommandLineErrorf("codeaction expects at least 1 argument")
	}
	cmd.app.editFlags = &cmd.EditFlags
	conn, err := cmd.app.connect(ctx)
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

	titleRE, err := regexp.Compile(cmd.Title)
	if err != nil {
		return err
	}

	// Get diagnostics, as they may encode various lazy code actions.
	if err := conn.diagnoseFiles(ctx, []protocol.DocumentURI{uri}); err != nil {
		return err
	}
	diagnostics := []protocol.Diagnostic{} // LSP wants non-nil slice
	conn.client.filesMu.Lock()
	diagnostics = append(diagnostics, file.diagnostics...)
	conn.client.filesMu.Unlock()

	// Request code actions of the desired kinds.
	var kinds []protocol.CodeActionKind
	if cmd.Kind != "" {
		for _, kind := range strings.Split(cmd.Kind, ",") {
			kinds = append(kinds, protocol.CodeActionKind(kind))
		}
	} else {
		kinds = append(kinds, protocol.Empty) // => all
	}
	actions, err := conn.CodeAction(ctx, &protocol.CodeActionParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: uri},
		Range:        rng,
		Context: protocol.CodeActionContext{
			Only:        kinds,
			Diagnostics: diagnostics,
		},
	})
	if err != nil {
		return fmt.Errorf("%v: %v", from, err)
	}

	// Gather edits from matching code actions.
	var edits []protocol.TextEdit
	for _, act := range actions {
		if act.Disabled != nil {
			continue
		}
		if !titleRE.MatchString(act.Title) {
			continue
		}

		// If the provided span has a position (not just offsets),
		// and the action has diagnostics, the action must have a
		// diagnostic with the same range as it.
		if from.HasPosition() && len(act.Diagnostics) > 0 &&
			!slices.ContainsFunc(act.Diagnostics, func(diag protocol.Diagnostic) bool {
				return diag.Range.Start == rng.Start
			}) {
			continue
		}

		if cmd.Exec {
			// -exec: run the first matching code action.
			if act.Command != nil {
				// This may cause the server to make
				// an ApplyEdit downcall to the client.
				if _, err := conn.executeCommand(ctx, act.Command); err != nil {
					return err
				}
				// The specification says that commands should
				// be executed _after_ edits are applied, not
				// instead of them, but we don't want to
				// duplicate edits.
			} else {
				// Partially apply CodeAction.Edit, a WorkspaceEdit.
				// (See also conn.Client.applyWorkspaceEdit(a.Edit)).
				for _, c := range act.Edit.DocumentChanges {
					tde := c.TextDocumentEdit
					if tde != nil && tde.TextDocument.URI == uri {
						// TODO(adonovan): this logic will butcher an edit that spans files.
						// It will also ignore create/delete/rename operations.
						// Fix or document. Need a three-way merge.
						edits = append(edits, protocol.AsTextEdits(tde.Edits)...)
					}
				}
				return applyTextEdits(file.mapper, edits, cmd.app.editFlags)
			}
			return nil
		} else {
			// No -exec: list matching code actions.
			action := "edit"
			if act.Command != nil {
				action = "command"
			}
			fmt.Printf("%s\t%q [%s]\n",
				action,
				act.Title,
				act.Kind)
		}
	}

	if cmd.Exec {
		return fmt.Errorf("no matching code action at %s", from)
	}
	return nil
}
