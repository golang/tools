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

	var changes []protocol.DocumentChange
	if namedArg(mark, "form0", "") != "" {
		changes, err = applyCodeActionForm(mark, kind, action)
	} else {
		changes, err = applyCodeAction(mark.run.env, action)
	}
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

func applyCodeActionForm(mark marker, _ string, action *protocol.CodeAction) ([]protocol.DocumentChange, error) {
	// Send an ExecuteCommand with command: "gopls/lsp" and a single argument
	// the argument has a "method": "command/resolve" and a "param", which
	// is the action with only the Command and Arguments field
	// Some types are not known to the protocol package
	type truncatedCodeAction struct {
		Command     string            `json:"command,omitempty"`
		Arguments   []json.RawMessage `json:"arguments,omitempty"`
		FormAnswers []string          `json:"formAnswers,omitempty"`
	}
	type resolveCommand struct {
		Method string
		Param  truncatedCodeAction
	}
	resolve := resolveCommand{
		Method: "command/resolve",
		Param: truncatedCodeAction{
			Command:   action.Command.Command,
			Arguments: action.Command.Arguments,
		},
	}
	// ExecuteCommand is called twice with a "gopls.lps" containing a resolveCommand,
	// the second time with the user response filled in. lspCmd factors out the
	// common code.
	lspCmd := func() (any, error) {
		buf, err := json.Marshal(resolve)
		if err != nil {
			return nil, err
		}
		exCmd := &protocol.ExecuteCommandParams{
			Command:   "gopls.lsp",
			Arguments: []json.RawMessage{buf},
		}
		ans, err := mark.run.env.Editor.Server.ExecuteCommand(mark.run.env.Ctx, exCmd)
		if err != nil {
			return nil, fmt.Errorf("%v %#v", err, ans)
		}
		return ans, nil
	}
	_, err := lspCmd()
	if err != nil {
		return nil, err
	}

	// now send the same command with a filled in "formAnswers" field
	for _, nm := range []string{"form0", "form1"} {
		if fm, ok := mark.note.NamedArgs[nm]; ok {
			buf, err := json.Marshal(fm)
			if err != nil || (false && len(buf) == 0) {
				return nil, fmt.Errorf("failed to marshal string %s, %v", fm, err)
			}
			resolve.Param.FormAnswers = append(resolve.Param.FormAnswers, fm.(string))
		} else {
			break // only want some of the form slots
		}
	}
	got, err := lspCmd()
	if err != nil {
		return nil, err
	}
	// got should have "command" which is the command out of the argument in second
	// and a single argument which describes what was done

	// catch the changes from the forthcoming ExecuteCommand
	var changes []protocol.DocumentChange
	restore := mark.run.env.Editor.Client().SetApplyEditHandler(func(ctx context.Context, wsedit *protocol.WorkspaceEdit) error {
		changes = append(changes, wsedit.DocumentChanges...)
		return nil
	})
	defer restore()
	// send the third ExecuteCommand with what we just got back in got
	mgot, ok := got.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("got %T from ExecuteCommand, expect map[string]any", got)
	}
	cmd, ok := mgot["command"].(string)
	if !ok {
		return nil, fmt.Errorf("expected command to be a string, got %T", mgot["command"])
	}
	gargs, ok := mgot["arguments"].([]any)
	if !ok {
		return nil, fmt.Errorf("expecte arguments to be []any, got %T", mgot["arguments"])
	}
	var args []json.RawMessage
	for _, g := range gargs {
		buf, err := json.Marshal(g)
		if err != nil {
			return nil, fmt.Errorf("Marshal failed, %v", err) // and maybe print g?
		}
		args = append(args, buf)
	}
	third := &protocol.ExecuteCommandParams{
		Command:   cmd,
		Arguments: args,
	}
	_, err = mark.run.env.Editor.Server.ExecuteCommand(mark.run.env.Ctx, third)
	if err != nil {
		return nil, fmt.Errorf("third ExecuteCommand failed %v", err)
	}
	// the edits were a side effect captured by the ApplyEditHandler

	return changes, nil
}

// not used for @codeaction, but codeactions

// codeAction executes a textDocument/codeAction request for the specified
// location and kind. If diag is non-nil, it is used as the code action
// context.
//
// The resulting map contains resulting file contents after the code action is
// applied. Currently, this function does not support code actions that return
// edits directly; it only supports code action commands.
func codeAction(env *integration.Env, uri protocol.DocumentURI, rng protocol.Range, kind protocol.CodeActionKind, diag *protocol.Diagnostic) (map[string][]byte, error) {
	action, err := resolveCodeAction(env, uri, rng, kind, diag)
	if err != nil {
		return nil, err
	}
	changes, err := applyCodeAction(env, action)
	if err != nil {
		return nil, err
	}
	return changedFiles(env, changes)
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
			//TriggerKind: protocol.CodeActionTriggerKind(1 /*Invoked*/),
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
func applyCodeAction(env *integration.Env, action *protocol.CodeAction) ([]protocol.DocumentChange, error) {
	// Collect any server-initiated changes created by workspace/applyEdit.
	//
	// We set up this handler immediately, not right before executing the code
	// action command, so we can assert that neither the codeAction request nor
	// codeAction resolve request cause edits as a side effect (golang/go#71405).
	var changes []protocol.DocumentChange
	restore := env.Editor.Client().SetApplyEditHandler(func(ctx context.Context, wsedit *protocol.WorkspaceEdit) error {
		changes = append(changes, wsedit.DocumentChanges...)
		return nil
	})
	defer restore()

	if action.Edit == nil && action.Command == nil {
		panic("bad action")
	}

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
			env.TB.Errorf("internal error: discarding unexpected CodeAction{Kind=%s, Title=%q}.Edit.Changes", action.Kind, action.Title)
		}
		if action.Edit.DocumentChanges != nil {
			if action.Command != nil {
				env.TB.Errorf("internal error: discarding unexpected CodeAction{Kind=%s, Title=%q}.Command", action.Kind, action.Title)
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

		if _, err := env.Editor.Server.ExecuteCommand(env.Ctx, &protocol.ExecuteCommandParams{
			Command:   action.Command.Command,
			Arguments: action.Command.Arguments,
		}); err != nil {
			return nil, err
		}
		return changes, nil // populated as a side effect of ExecuteCommand
	}

	return nil, nil
}
