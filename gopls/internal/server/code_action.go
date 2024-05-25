// Copyright 2018 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package server

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"golang.org/x/tools/gopls/internal/cache"
	"golang.org/x/tools/gopls/internal/file"
	"golang.org/x/tools/gopls/internal/golang"
	"golang.org/x/tools/gopls/internal/mod"
	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/gopls/internal/protocol/command"
	"golang.org/x/tools/gopls/internal/util/slices"
	"golang.org/x/tools/internal/event"
)

func (s *server) CodeAction(ctx context.Context, params *protocol.CodeActionParams) ([]protocol.CodeAction, error) {
	ctx, done := event.Start(ctx, "lsp.Server.codeAction")
	defer done()

	fh, snapshot, release, err := s.fileOf(ctx, params.TextDocument.URI)
	if err != nil {
		return nil, err
	}
	defer release()
	uri := fh.URI()

	// Determine the supported actions for this file kind.
	kind := snapshot.FileKind(fh)
	supportedCodeActions, ok := snapshot.Options().SupportedCodeActions[kind]
	if !ok {
		return nil, fmt.Errorf("no supported code actions for %v file kind", kind)
	}
	if len(supportedCodeActions) == 0 {
		return nil, nil // not an error if there are none supported
	}

	// The Only field of the context specifies which code actions the client wants.
	// If Only is empty, assume that the client wants all of the non-explicit code actions.
	want := supportedCodeActions
	if len(params.Context.Only) > 0 {
		want = make(map[protocol.CodeActionKind]bool)

		// Explicit Code Actions are opt-in and shouldn't be
		// returned to the client unless requested using Only.
		//
		// This mechanim exists to avoid a distracting
		// lightbulb (code action) on each Test function.
		// These actions are unwanted in VS Code because it
		// has Test Explorer, and in other editors because
		// the UX of executeCommand is unsatisfactory for tests:
		// it doesn't show the complete streaming output.
		// See https://github.com/joaotavora/eglot/discussions/1402
		// for a better solution.
		explicit := map[protocol.CodeActionKind]bool{
			protocol.GoTest: true,
		}

		for _, only := range params.Context.Only {
			for k, v := range supportedCodeActions {
				if only == k || strings.HasPrefix(string(k), string(only)+".") {
					want[k] = want[k] || v
				}
			}
			want[only] = want[only] || explicit[only]
		}
	}
	if len(want) == 0 {
		return nil, fmt.Errorf("no supported code action to execute for %s, wanted %v", uri, params.Context.Only)
	}

	switch kind {
	case file.Mod:
		var actions []protocol.CodeAction

		fixes, err := s.codeActionsMatchingDiagnostics(ctx, fh.URI(), snapshot, params.Context.Diagnostics, want)
		if err != nil {
			return nil, err
		}

		// Group vulnerability fixes by their range, and select only the most
		// appropriate upgrades.
		//
		// TODO(rfindley): can this instead be accomplished on the diagnosis side,
		// so that code action handling remains uniform?
		vulnFixes := make(map[protocol.Range][]protocol.CodeAction)
	searchFixes:
		for _, fix := range fixes {
			for _, diag := range fix.Diagnostics {
				if diag.Source == string(cache.Govulncheck) || diag.Source == string(cache.Vulncheck) {
					vulnFixes[diag.Range] = append(vulnFixes[diag.Range], fix)
					continue searchFixes
				}
			}
			actions = append(actions, fix)
		}

		for _, fixes := range vulnFixes {
			fixes = mod.SelectUpgradeCodeActions(fixes)
			actions = append(actions, fixes...)
		}

		return actions, nil

	case file.Go:
		// diagnostic-bundled code actions
		//
		// The diagnostics already have a UI presence (e.g. squiggly underline);
		// the associated action may additionally show (in VS Code) as a lightbulb.
		// Note s.codeActionsMatchingDiagnostics returns only fixes
		// detected during the analysis phase. golang.CodeActions computes
		// extra changes that can address some diagnostics.
		actions, err := s.codeActionsMatchingDiagnostics(ctx, uri, snapshot, params.Context.Diagnostics, want)
		if err != nil {
			return nil, err
		}

		// computed code actions (may include quickfixes from diagnostics)
		trigger := protocol.CodeActionUnknownTrigger
		if k := params.Context.TriggerKind; k != nil { // (some clients omit it)
			trigger = *k
		}
		moreActions, err := golang.CodeActions(ctx, snapshot, fh, params.Range, params.Context.Diagnostics, want, trigger)
		if err != nil {
			return nil, err
		}
		actions = append(actions, moreActions...)

		// Don't suggest fixes for generated files, since they are generally
		// not useful and some editors may apply them automatically on save.
		// (Unfortunately there's no reliable way to distinguish fixes from
		// queries, so we must list all kinds of queries here.)
		if golang.IsGenerated(ctx, snapshot, uri) {
			actions = slices.DeleteFunc(actions, func(a protocol.CodeAction) bool {
				switch a.Kind {
				case protocol.GoTest,
					protocol.GoDoc,
					protocol.GoFreeSymbols,
					protocol.GoAssembly:
					return false // read-only query
				}
				return true // potential write operation
			})
		}

		return actions, nil

	default:
		// Unsupported file kind for a code action.
		return nil, nil
	}
}

// ResolveCodeAction resolves missing Edit information (that is, computes the
// details of the necessary patch) in the given code action using the provided
// Data field of the CodeAction, which should contain the raw json of a protocol.Command.
//
// This should be called by the client before applying code actions, when the
// client has code action resolve support.
//
// This feature allows capable clients to preview and selectively apply the diff
// instead of applying the whole thing unconditionally through workspace/applyEdit.
func (s *server) ResolveCodeAction(ctx context.Context, ca *protocol.CodeAction) (*protocol.CodeAction, error) {
	ctx, done := event.Start(ctx, "lsp.Server.resolveCodeAction")
	defer done()

	// Only resolve the code action if there is Data provided.
	var cmd protocol.Command
	if ca.Data != nil {
		if err := protocol.UnmarshalJSON(*ca.Data, &cmd); err != nil {
			return nil, err
		}
	}
	if cmd.Command != "" {
		params := &protocol.ExecuteCommandParams{
			Command:   cmd.Command,
			Arguments: cmd.Arguments,
		}

		handler := &commandHandler{
			s:      s,
			params: params,
		}
		edit, err := command.Dispatch(ctx, params, handler)
		if err != nil {

			return nil, err
		}
		var ok bool
		if ca.Edit, ok = edit.(*protocol.WorkspaceEdit); !ok {
			return nil, fmt.Errorf("unable to resolve code action %q", ca.Title)
		}
	}
	return ca, nil
}

// codeActionsMatchingDiagnostics creates code actions for the
// provided diagnostics, by unmarshalling actions bundled in the
// protocol.Diagnostic.Data field or, if there were none, by creating
// actions from edits associated with a matching Diagnostic from the
// set of stored diagnostics for this file.
func (s *server) codeActionsMatchingDiagnostics(ctx context.Context, uri protocol.DocumentURI, snapshot *cache.Snapshot, pds []protocol.Diagnostic, want map[protocol.CodeActionKind]bool) ([]protocol.CodeAction, error) {
	var actions []protocol.CodeAction
	var unbundled []protocol.Diagnostic // diagnostics without bundled code actions in their Data field
	for _, pd := range pds {
		bundled := cache.BundledLazyFixes(pd)
		if len(bundled) > 0 {
			for _, fix := range bundled {
				if want[fix.Kind] {
					actions = append(actions, fix)
				}
			}
		} else {
			// No bundled actions: keep searching for a match.
			unbundled = append(unbundled, pd)
		}
	}

	for _, pd := range unbundled {
		for _, sd := range s.findMatchingDiagnostics(uri, pd) {
			diagActions, err := codeActionsForDiagnostic(ctx, snapshot, sd, &pd, want)
			if err != nil {
				return nil, err
			}
			actions = append(actions, diagActions...)
		}
	}
	return actions, nil
}

func codeActionsForDiagnostic(ctx context.Context, snapshot *cache.Snapshot, sd *cache.Diagnostic, pd *protocol.Diagnostic, want map[protocol.CodeActionKind]bool) ([]protocol.CodeAction, error) {
	var actions []protocol.CodeAction
	for _, fix := range sd.SuggestedFixes {
		if !want[fix.ActionKind] {
			continue
		}
		var changes []protocol.DocumentChange
		for uri, edits := range fix.Edits {
			fh, err := snapshot.ReadFile(ctx, uri)
			if err != nil {
				return nil, err
			}
			change := protocol.DocumentChangeEdit(fh, edits)
			changes = append(changes, change)
		}
		actions = append(actions, protocol.CodeAction{
			Title:       fix.Title,
			Kind:        fix.ActionKind,
			Edit:        protocol.NewWorkspaceEdit(changes...),
			Command:     fix.Command,
			Diagnostics: []protocol.Diagnostic{*pd},
		})
	}
	return actions, nil
}

func (s *server) findMatchingDiagnostics(uri protocol.DocumentURI, pd protocol.Diagnostic) []*cache.Diagnostic {
	s.diagnosticsMu.Lock()
	defer s.diagnosticsMu.Unlock()

	var sds []*cache.Diagnostic
	for _, viewDiags := range s.diagnostics[uri].byView {
		for _, sd := range viewDiags.diagnostics {
			sameDiagnostic := (pd.Message == strings.TrimSpace(sd.Message) && // extra space may have been trimmed when converting to protocol.Diagnostic
				protocol.CompareRange(pd.Range, sd.Range) == 0 &&
				pd.Source == string(sd.Source))

			if sameDiagnostic {
				sds = append(sds, sd)
			}
		}
	}
	return sds
}

func (s *server) getSupportedCodeActions() []protocol.CodeActionKind {
	allCodeActionKinds := make(map[protocol.CodeActionKind]struct{})
	for _, kinds := range s.Options().SupportedCodeActions {
		for kind := range kinds {
			allCodeActionKinds[kind] = struct{}{}
		}
	}
	var result []protocol.CodeActionKind
	for kind := range allCodeActionKinds {
		result = append(result, kind)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i] < result[j]
	})
	return result
}

type unit = struct{}
