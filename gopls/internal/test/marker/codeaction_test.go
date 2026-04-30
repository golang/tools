// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package marker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"

	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/gopls/internal/protocol/command"
	"golang.org/x/tools/gopls/internal/test/integration"
	"golang.org/x/tools/internal/expect"
)

func codeActionMarker(mark marker, loc protocol.Location, kind string) {
	if !exactlyOneNamedArg(mark, "action", "edit", "result", "err") {
		return
	}

	if end := namedArgFunc(mark, "end", convertNamedArgLocation, protocol.Location{}); end.URI != "" {
		if end.URI != loc.URI {
			mark.errorf("end marker is in a different file (%s)", filepath.Base(loc.URI.Path()))
			return
		}
		loc.Range.End = end.Range.End
	}

	var (
		edit       = namedArg(mark, "edit", expect.Identifier(""))
		result     = namedArg(mark, "result", expect.Identifier(""))
		wantAction = namedArg(mark, "action", expect.Identifier(""))
		wantErr    = namedArgFunc(mark, "err", convertStringMatcher, stringMatcher{})
	)

	var diag *protocol.Diagnostic
	if re := namedArg(mark, "diag", (*regexp.Regexp)(nil)); re != nil {
		d, ok := removeDiagnostic(mark, loc, false, re)
		if !ok {
			mark.errorf("no diagnostic at %v matches %q", loc, re)
			return
		}
		diag = &d
	}

	action, err := resolveCodeAction(mark.run.env, loc.URI, loc.Range, protocol.CodeActionKind(kind), diag)
	if err != nil {
		if !wantErr.empty() {
			wantErr.checkErr(mark, err)
		} else {
			mark.errorf("resolveCodeAction failed: %v", err)
		}
		return
	}

	// When 'action' is set, we simply compare the action, and don't apply it.
	if wantAction != "" {
		g := mark.getGolden(wantAction)
		if action == nil {
			mark.errorf("no matching codeAction")
			return
		}
		data, err := json.MarshalIndent(action, "", "\t")
		if err != nil {
			mark.errorf("failed to marshal codeaction: %v", err)
			return
		}
		data = bytes.ReplaceAll(data, []byte(mark.run.env.Sandbox.Workdir.RootURI()), []byte("$WORKDIR"))
		compareGolden(mark, data, g)
		return
	}

	changes, err := applyCodeAction(mark, action)
	if err != nil {
		if !wantErr.empty() {
			wantErr.checkErr(mark, err)
		} else {
			mark.errorf("codeAction failed: %v", err)
		}
		return
	}

	changed, err := changedFiles(mark.run.env, changes)
	if err != nil {
		mark.errorf("changedFiles failed: %v", err)
		return
	}

	switch {
	case edit != "":
		g := mark.getGolden(edit)
		checkDiffs(mark, changed, g)
	case result != "":
		g := mark.getGolden(result)
		// Check the file state.
		checkChangedFiles(mark, changed, g)
	case !wantErr.empty():
		wantErr.checkErr(mark, err)
	default:
		panic("unreachable")
	}
}

func exactlyOneNamedArg(mark marker, names ...string) bool {
	var found []string
	for _, name := range names {
		if _, ok := mark.note.NamedArgs[name]; ok {
			found = append(found, name)
		}
	}
	if len(found) != 1 {
		mark.errorf("need exactly one of %v to be set, got %v", names, found)
		return false
	}
	return true
}

// not used for @codeaction, but codeactions

// codeAction executes a textDocument/codeAction request for the specified
// location and kind. If diag is non-nil, it is used as the code action
// context.
//
// The resulting map contains resulting file contents after the code action is
// applied. Currently, this function does not support code actions that return
// edits directly; it only supports code action commands.
func codeAction(mark marker, uri protocol.DocumentURI, rng protocol.Range, kind protocol.CodeActionKind, diag *protocol.Diagnostic) (map[string][]byte, error) {
	action, err := resolveCodeAction(mark.run.env, uri, rng, kind, diag)
	if err != nil {
		return nil, err
	}
	changes, err := applyCodeAction(mark, action)
	if err != nil {
		return nil, err
	}
	return changedFiles(mark.run.env, changes)
}

// resolveCodeAction resolves the code action specified by the given location,
// kind, and diagnostic.
func resolveCodeAction(env *integration.Env, uri protocol.DocumentURI, rng protocol.Range, kind protocol.CodeActionKind, diag *protocol.Diagnostic) (*protocol.CodeAction, error) {
	// Request all code actions that apply to the diagnostic.
	// A production client would set Only=[kind],
	// but we can give a better error if we don't filter.
	params := &protocol.CodeActionParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: uri},
		Range:        rng,
		Context: protocol.CodeActionContext{
			Only: []protocol.CodeActionKind{protocol.Empty}, // => all
			// TriggerKind: protocol.CodeActionTriggerKind(1 /*Invoked*/),
		},
	}
	if diag != nil {
		params.Context.Diagnostics = []protocol.Diagnostic{*diag}
	}

	actions, err := env.Editor.Server.CodeAction(env.Ctx, params)
	if err != nil {
		return nil, err
	}

	// Find the sole candidate CodeAction of exactly the specified kind
	// (e.g. refactor.inline.call).
	var candidates []protocol.CodeAction
	for _, act := range actions {
		if act.Kind == kind {
			candidates = append(candidates, act)
		}
	}
	if len(candidates) != 1 {
		var msg bytes.Buffer
		fmt.Fprintf(&msg, "found %d CodeActions of kind %s for this diagnostic, want 1", len(candidates), kind)
		for _, act := range actions {
			fmt.Fprintf(&msg, "\n\tfound %q (%s)", act.Title, act.Kind)
		}
		return nil, errors.New(msg.String())
	}
	action := candidates[0]

	// Resolve code action edits first if the client has resolve support
	// and the code action has no edits.
	if action.Edit == nil {
		editSupport, err := env.Editor.EditResolveSupport()
		if err != nil {
			return nil, err
		}
		if editSupport {
			resolved, err := env.Editor.Server.ResolveCodeAction(env.Ctx, &action)
			if err != nil {
				return nil, err
			}
			action = *resolved
		}
	}

	return &action, nil
}

// applyCodeAction applies the given code action, and captures the resulting
// document changes. This is not called for forms.
func applyCodeAction(mark marker, action *protocol.CodeAction) ([]protocol.DocumentChange, error) {
	if action.Edit == nil && action.Command == nil {
		return nil, fmt.Errorf("bad action: the server returned a CodeAction %q with no Edit and no Command", action.Title)
	}

	// Collect any server-initiated changes created by workspace/applyEdit.
	//
	// We set up this handler immediately, not right before executing the code
	// action command, so we can assert that neither the codeAction request nor
	// codeAction resolve request cause edits as a side effect (golang/go#71405).
	var changes []protocol.DocumentChange
	restore := mark.run.env.Editor.Client().SetApplyEditHandler(func(ctx context.Context, wsedit *protocol.WorkspaceEdit) error {
		changes = append(changes, wsedit.DocumentChanges...)
		return nil
	})
	defer restore()

	// Apply the codeAction.
	//
	// Spec:
	//  "If a code action provides an edit and a command, first the edit is
	//  executed and then the command."
	// An action may specify an edit and/or a command, to be
	// applied in that order. But since applyDocumentChanges(env,
	// action.Edit.DocumentChanges) doesn't compose, for now we
	// assert that actions return one or the other.
	if action.Edit != nil {
		if len(action.Edit.Changes) > 0 {
			mark.run.env.TB.Errorf("internal error: discarding unexpected CodeAction{Kind=%s, Title=%q}.Edit.Changes", action.Kind, action.Title)
		}
		if action.Edit.DocumentChanges != nil {
			if action.Command != nil {
				mark.run.env.TB.Errorf("internal error: discarding unexpected CodeAction{Kind=%s, Title=%q}.Command", action.Kind, action.Title)
			}
			return action.Edit.DocumentChanges, nil
		}
	}

	if action.Command != nil {
		// This is a typical CodeAction command:
		//
		//   Title:     "Implement error"
		//   Command:   gopls.apply_fix
		//   Arguments: [{"Fix":"stub_methods","URI":".../a.go","Range":...}}]
		//
		// The client makes an ExecuteCommand RPC to the server,
		// which dispatches it to the ApplyFix handler.
		// ApplyFix dispatches to the "stub_methods" fixer (the meat).
		// The server then makes an ApplyEdit RPC to the client,
		// whose WorkspaceEditFunc hook temporarily gathers the edits
		// instead of applying them.

		// resolveCommand simulates how a client (like vscode-go) resolves interactive
		// commands using a tunneling mechanism.
		//
		// Because some clients cannot send custom JSON-RPC methods directly, they tunnel
		// a "command/resolve" request via the standard "workspace/executeCommand"
		// method (specifically using the "gopls.lsp" command). This function replicates
		// that JSON tunneling rather than calling [protocol.Server.ResolveCommand]
		// directly, ensuring the actual over-the-wire pipeline is tested.
		resolveCommand := func(unresolved *protocol.ExecuteCommandParams) (resolved *protocol.ExecuteCommandParams, err error) {
			unresolvedJSON, err := json.Marshal(unresolved)
			if err != nil {
				return nil, fmt.Errorf("failed to marshal unresolved command: %v", err)
			}

			lspArgJSON, err := json.Marshal(command.LSPArgs{
				Method: "command/resolve",
				Param:  unresolvedJSON,
			})
			if err != nil {
				return nil, fmt.Errorf("failed to marshal LSPArgs wrapper: %v", err)
			}

			lspCmd := &protocol.ExecuteCommandParams{
				Command:   "gopls.lsp",
				Arguments: []json.RawMessage{lspArgJSON},
			}

			rawResponse, err := mark.run.env.Editor.Server.ExecuteCommand(mark.run.env.Ctx, lspCmd)
			if err != nil {
				return nil, fmt.Errorf("server.ExecuteCommand failed: %v", err)
			}

			responseJSON, err := json.Marshal(rawResponse)
			if err != nil {
				return nil, fmt.Errorf("failed to marshal server response: %v", err)
			}

			if err := json.Unmarshal(responseJSON, &resolved); err != nil {
				return nil, fmt.Errorf("failed to unmarshal resolved command: %v", err)
			}

			return resolved, nil
		}

		cmd, err := resolveCommand(&protocol.ExecuteCommandParams{
			Command:   action.Command.Command,
			Arguments: action.Command.Arguments,
		})
		if err != nil {
			return nil, err
		}

		// Check if the command interactivity is required.
		if len(cmd.FormFields) > 0 {
			// Fill in "formAnswers" field from named arg "formX".
			for _, name := range []string{"form0", "form1"} { // Test forms have at most two fields.
				arg, ok := mark.note.NamedArgs[name]
				if !ok {
					break // No more answers provided by the test.
				}

				// TODO(hxjiang): support other kind of inputs.
				cmd.FormAnswers = append(cmd.FormAnswers, arg.(string))
			}

			// Re-resolve command with the "formAnswers" field filled.
			cmd, err = resolveCommand(cmd)
			if err != nil {
				return nil, err
			}

			if len(cmd.FormFields) > 0 {
				return nil, fmt.Errorf("got %v question after providing answers, expect 0", len(cmd.FormFields))
			}
		}

		if _, err := mark.run.env.Editor.Server.ExecuteCommand(mark.run.env.Ctx, cmd); err != nil {
			return nil, err
		}
		return changes, nil // populated as a side effect of ExecuteCommand
	}

	return nil, nil
}
