// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package server

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/gopls/internal/protocol/command"
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

// In vscode-go, because it is a standard LSP client,
// cannot do step 4 as described. Instead
// it calls "workspace/executeCommand" with command 'gopls.lsp'
// and parameter.Method "command/resolve".
//
// ExecuteCommand() calls command.Dispatch() which calls LSP()
// which calls protocol.ServerDispatchCall("command/resolve")
// which calls ResolveCommand. command.Dispatch() switches on param.Command and LSP() passes param.Method
// to ServerDispatchCall

// The neovim flow is simpler, as 'command/resolve' can be invoked directly.

var AddTagsForm = []protocol.FormField{
	{
		Description: `comma-separated list of tags to add; e.g.. "json,xml"`,
		Type:        protocol.FormFieldTypeString{Kind: "string"},
		Default:     "json",
	},
	{
		Description: `transform rule for added tags, e.g., "camelcase' or 'snakecase"`,
		Type: protocol.FormFieldTypeEnum{
			Kind: "enum",
			Name: "transform rule",
			Values: []string{
				"camelcase",  // MyField -> myField
				"lispcase",   // MyField -> my-field
				"pascalcase", // MyField -> MyField
				"titlecase",  // MyField -> My Field
				"snakecase",  // MyField -> my_field
				"keep",       // keep the existing field name
			},
		},
		Default: "camelcase",
	},
}

var RemoveTagsForm = []protocol.FormField{
	{
		Description: `comma-separated list of tags to remove; e.g., "json,xml"`,
		Type:        protocol.FormFieldTypeString{Kind: "string"},
		Default:     "json", // TODO(?): put the existing tags here?
	},
}

// ResolveComamnd is called indirectly from WorkspaceCommand(), explained above.
func (s *server) ResolveCommand(ctx context.Context, param *protocol.ExecuteCommandParams) (*protocol.ExecuteCommandParams, error) {
	switch param.Command {
	case "gopls.modify_tags":
		return resolveModifyTags(param)
	}
	return nil, notImplemented(fmt.Sprintf("ResolveCommand(%s)", param.Command))
}

func resolveModifyTags(param *protocol.ExecuteCommandParams) (*protocol.ExecuteCommandParams, error) {
	var a0 command.ModifyTagsArgs
	if err := command.UnmarshalArgs(param.Arguments, &a0); err != nil {
		return nil, err
	}
	switch a0.Modification {
	case "add":
		switch len(param.FormAnswers) {
		case 0: // first call, return the form
			param.FormFields = AddTagsForm
			return param, nil
		case 2: // second call, process the form
			var ok bool
			if a0.Add, ok = param.FormAnswers[0].(string); !ok {
				return nil, fmt.Errorf("invalid type of first value, want string: %v", param.FormAnswers[0])
			}

			if slices.Contains(strings.Split(a0.Add, ","), "") {
				// TODO(pjw): instead filter AddTagsForm to remove empty tags and tags containing spaces
				form := slices.Clone(AddTagsForm)
				form[0].Error = "input tags should not contain empty tag"
				param.FormFields = form
				return param, nil
			}

			if a0.Transform, ok = param.FormAnswers[1].(string); !ok {
				return nil, fmt.Errorf("invalid type of second value, want string: %v", param.FormAnswers[1])
			}
			// PJW: what happens when the user enters a bad value? (i think the client handles it)

			raw, err := command.MarshalArgs(a0)
			if err != nil {
				return nil, err
			}

			param.FormAnswers = nil
			param.FormFields = nil
			param.Arguments = raw
			return param, nil
		default:
			return nil, fmt.Errorf("modify tags command expecting 1 value from client, got %v", len(param.FormAnswers))
		}
	case "remove":
		switch len(param.FormAnswers) {
		case 0: // first call, return the form
			// TODO? show the user the current list of tags?
			param.FormFields = RemoveTagsForm
			return param, nil
		case 1: // second call, process the form
			var ok bool
			if a0.Remove, ok = param.FormAnswers[0].(string); !ok {
				return nil, fmt.Errorf("invalid type of value, want string: %v", param.FormAnswers[0])
			}
			raw, err := command.MarshalArgs(a0)
			if err != nil {
				return nil, err
			}

			param.FormAnswers = nil
			param.FormFields = nil
			param.Arguments = raw
			return param, nil
		default:
			return nil, fmt.Errorf("modify tags command expecting 1 value from client, got %v", len(param.FormAnswers))
		}
	default:
		return nil, fmt.Errorf("unsupported modify tags operation: %s", a0.Modification)
	}
}
