// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package golang

import (
	"context"
	"fmt"
	"slices"

	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/gopls/internal/protocol/command"
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

func resolveModifyTags(options settings.ClientOptions, param *protocol.ExecuteCommandParams) error {
	var a0 command.ModifyTagsArgs
	if err := command.UnmarshalArgs(param.Arguments, &a0); err != nil {
		return err
	}
	switch a0.Modification {
	case "add":
		if !supportsDialog(options, addTagsForm) {
			return nil
		}

		// First call, return the form.
		if len(param.FormAnswers) == 0 {
			param.FormFields = addTagsForm
			return nil
		}

		v0, err := param.RequiredAnswer[string]("tags")
		if err != nil {
			return err
		}

		if _, err = SanitizeTags(v0); err != nil {
			form := slices.Clone(addTagsForm)
			form[0].Error = err.Error()
			param.FormFields = form
			return nil
		}

		if _, err = param.RequiredAnswer[string]("transform"); err != nil {
			return err
		}
		// PJW: what happens when the user enters a bad value? (i think the client handles it)

		param.FormFields = nil
		return nil
	case "remove":
		if !supportsDialog(options, removeTagsForm) {
			return nil
		}

		// First call, return the form
		if len(param.FormAnswers) == 0 {
			// TODO? show the user the current list of tags?
			param.FormFields = removeTagsForm
			return nil
		}

		v, err := param.RequiredAnswer[string]("tags")
		if err != nil {
			return err
		}
		if _, err := SanitizeTags(v); err != nil {
			form := slices.Clone(addTagsForm)
			form[0].Error = err.Error()
			param.FormFields = form
			return nil
		}

		param.FormFields = nil
		return nil
	default:
		return fmt.Errorf("unsupported modify tags operation: %s", a0.Modification)
	}
}

func resolveImplementInterface(options settings.ClientOptions, param *protocol.ExecuteCommandParams) error {
	var a0 command.ImplementInterfaceArgs
	if err := command.UnmarshalArgs(param.Arguments, &a0); err != nil {
		return err
	}

	var form []protocol.FormField
	if ok := options.SupportedInteractiveInputTypes[protocol.FormFieldKindLazyEnum]; ok {
		form = implementInterfaceFormLazyEnum
	} else if ok := options.SupportedInteractiveInputTypes[protocol.FormFieldKindString]; ok {
		form = implementInterfaceFormString
	} else {
		// This should not happen, as the gopls should not offer such code
		// action if the language client does not support any kind above.
		return fmt.Errorf("internal error: unsupported interactive input types: %v", options.SupportedInteractiveInputTypes)
	}

	// First call, return the empty form.
	if len(param.FormAnswers) == 0 {
		param.FormFields = form
		return nil
	}

	v, err := param.RequiredAnswer[string]("interface")
	if err != nil {
		return err
	}

	if err := validInterfaceName(v); err != nil {
		// The client only sends back answers, not the original form fields.
		// Clone the static form template so we can attach the validation
		// error and send the complete form back for the client to re-render.
		form := slices.Clone(form)
		form[0].Error = err.Error()
		param.FormFields = form
		return nil
	}

	param.FormFields = nil
	return nil
}

func resolveMoveDeclaration(options settings.ClientOptions, param *protocol.ExecuteCommandParams) error {
	var a0 command.MoveDeclarationArgs
	if err := command.UnmarshalArgs(param.Arguments, &a0); err != nil {
		return err
	}
	var form []protocol.FormField
	if ok := options.SupportedInteractiveInputTypes[protocol.FormFieldKindFile]; ok {
		form = moveDeclarationFormFile
	} else if ok := options.SupportedInteractiveInputTypes[protocol.FormFieldKindString]; ok {
		form = moveDeclarationFormString
	} else {
		// This should not happen because gopls should not offer this code action if the
		// language client does not support any kind above.
		return fmt.Errorf("internal error: unsupported interactive input types: %v", options.SupportedInteractiveInputTypes)
	}

	// First call, return the empty form.
	if len(param.FormAnswers) == 0 {
		param.FormFields = form
		return nil
	}

	file, err := param.RequiredAnswer[string]("file")
	if err != nil {
		return err
	}
	if _, err := protocol.ParseDocumentURI(file); err != nil {
		return err
	}
	param.FormFields = nil
	return nil
}
