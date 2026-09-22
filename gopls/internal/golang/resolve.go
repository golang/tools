// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package golang

import (
	"context"

	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/gopls/internal/settings"
)

// Ths file contains the code to mediate user dialogs in the client

// The logical flow is as follows:
// 1. Client sends 'textDocument/codeAction'
//    gopls respond with one or more CodeActions, including one with kind either
//    'refactor.rewrite.addTags' or 'refactor.rewrite.removeTags'
// 2. Client sends 'codeAction/resolve' with the selected CodeActions
//    gopls returns the same CodeAction
// 3. Client sends 'workspace/executeCommand' to get the dialog form
//    gopls responds with the form to display to the user
// 4. Client sends 'command/resolve' with the filled out form
//    gopls responds, with 'workspace/applyEdit' saying what to do
// 5. Client return ApplyWorkspaceEditResult with applied = true
// 6. Client sends a textDocument/didChange notification, with the edits applied (optional)

// ResolveCommand implements the interactive resolution step for workspace commands.
// It inspects the command name within the provided [protocol.ExecuteCommandParams]
// and delegates to the appropriate command-specific resolver (e.g., modify_tags,
// implement_interface).
//
// For full details on the interactive protocol, the multi-step handshake, and the
// conditions under which the parameter is modified or returned as-is, see the
// official documentation on the protocol interface and [protocol.Server.ResolveCommand].
func ResolveCommand(ctx context.Context, params *protocol.ExecuteCommandParams, options settings.ClientOptions) (*protocol.ExecuteCommandParams, error) {
	switch params.Command {
	case "gopls.modify_tags":
		if err := resolveModifyTags(options, params); err != nil {
			return nil, err
		}
	case "gopls.implement_interface":
		if err := resolveImplementInterface(options, params); err != nil {
			return nil, err
		}
	case "gopls.move_declaration":
		if err := resolveMoveDeclaration(options, params); err != nil {
			return nil, err
		}
	}
	return params, nil
}
