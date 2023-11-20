// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package cache

import (
	"encoding/json"

	"golang.org/x/tools/gopls/internal/bug"
	"golang.org/x/tools/gopls/internal/lsp/protocol"
)

// SuggestedFixFromCommand returns a suggested fix to run the given command.
func SuggestedFixFromCommand(cmd protocol.Command, kind protocol.CodeActionKind) SuggestedFix {
	return SuggestedFix{
		Title:      cmd.Title,
		Command:    &cmd,
		ActionKind: kind,
	}
}

// quickFixesJSON is a JSON-serializable list of quick fixes
// to be saved in the protocol.Diagnostic.Data field.
type quickFixesJSON struct {
	// TODO(rfindley): pack some sort of identifier here for later
	// lookup/validation?
	Fixes []protocol.CodeAction
}

// BundleQuickFixes attempts to bundle sd.SuggestedFixes into the
// sd.BundledFixes field, so that it can be round-tripped through the client.
// It returns false if the quick-fixes cannot be bundled.
func BundleQuickFixes(sd *Diagnostic) bool {
	if len(sd.SuggestedFixes) == 0 {
		return true
	}
	var actions []protocol.CodeAction
	for _, fix := range sd.SuggestedFixes {
		if fix.Edits != nil {
			// For now, we only support bundled code actions that execute commands.
			//
			// In order to cleanly support bundled edits, we'd have to guarantee that
			// the edits were generated on the current snapshot. But this naively
			// implies that every fix would have to include a snapshot ID, which
			// would require us to republish all diagnostics on each new snapshot.
			//
			// TODO(rfindley): in order to avoid this additional chatter, we'd need
			// to build some sort of registry or other mechanism on the snapshot to
			// check whether a diagnostic is still valid.
			return false
		}
		action := protocol.CodeAction{
			Title:   fix.Title,
			Kind:    fix.ActionKind,
			Command: fix.Command,
		}
		actions = append(actions, action)
	}
	fixes := quickFixesJSON{
		Fixes: actions,
	}
	data, err := json.Marshal(fixes)
	if err != nil {
		bug.Reportf("marshalling quick fixes: %v", err)
		return false
	}
	msg := json.RawMessage(data)
	sd.BundledFixes = &msg
	return true
}

// BundledQuickFixes extracts any bundled codeActions from the
// diag.Data field.
func BundledQuickFixes(diag protocol.Diagnostic) []protocol.CodeAction {
	if diag.Data == nil {
		return nil
	}
	var fix quickFixesJSON
	if err := json.Unmarshal(*diag.Data, &fix); err != nil {
		bug.Reportf("unmarshalling quick fix: %v", err)
		return nil
	}

	var actions []protocol.CodeAction
	for _, action := range fix.Fixes {
		// See BundleQuickFixes: for now we only support bundling commands.
		if action.Edit != nil {
			bug.Reportf("bundled fix %q includes workspace edits", action.Title)
			continue
		}
		// associate the action with the incoming diagnostic
		// (Note that this does not mutate the fix.Fixes slice).
		action.Diagnostics = []protocol.Diagnostic{diag}
		actions = append(actions, action)
	}

	return actions
}
