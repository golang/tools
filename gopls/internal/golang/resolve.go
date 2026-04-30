// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package golang

import (
	"context"
	"encoding/json"
	"fmt"
	"go/token"
	"slices"
	"strings"
	"unicode"

	"golang.org/x/mod/module"
	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/gopls/internal/protocol/command"
	"golang.org/x/tools/gopls/internal/settings"
	"golang.org/x/tools/gopls/internal/util/morestrings"
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

// In vscode-go, because it is a standard LSP client, cannot do step 4 as
// described. Instead it calls "workspace/executeCommand" with command
// 'gopls.lsp' and parameter.Method "command/resolve".
//
// ExecuteCommand() calls command.Dispatch() which calls LSP()
// which calls protocol.ServerDispatchCall("command/resolve")
// which calls ResolveCommand. command.Dispatch() switches on param.Command and LSP() passes param.Method
// to ServerDispatchCall

// The neovim flow is simpler, as 'command/resolve' can be invoked directly.

var addTagsForm = []protocol.FormField{
	{
		Description: `comma-separated list of tags to add; e.g.. "json,xml"`,
		Type:        protocol.FormFieldTypeString{Kind: "string"},
		Default:     "json",
	},
	{
		Description: `transform rule for added tags, e.g., "camelcase' or 'snakecase"`,
		Type: protocol.FormFieldTypeEnum{
			Kind: "enum",
			Entries: []protocol.FormEnumEntry{
				{
					Value:       "camelcase",
					Description: "camelCase",
				},
				{
					Value:       "lispcase",
					Description: "lisp-case",
				},
				{
					Value:       "pascalcase",
					Description: "PascalCase",
				},
				{
					Value:       "titlecase",
					Description: "Title Case",
				},
				{
					Value:       "snakecase",
					Description: "snake_case",
				},
			},
		},
		Default: "camelcase",
	},
}

var removeTagsForm = []protocol.FormField{
	{
		Description: `comma-separated list of tags to remove; e.g., "json,xml"`,
		Type:        protocol.FormFieldTypeString{Kind: "string"},
		Default:     "json", // TODO(?): put the existing tags here?
	},
}

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

		// User parameter 0.
		v0, err := FormAnswer[string](&param.InteractiveParams, 0)
		if err != nil {
			return err
		}
		if _, err = SanitizeTags(v0); err != nil {
			form := slices.Clone(addTagsForm)
			form[0].Error = err.Error()
			param.FormFields = form
			return nil
		}

		// User parameter 1.
		_, err = FormAnswer[string](&param.InteractiveParams, 1)
		if err != nil {
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

		v, err := FormAnswer[string](&param.InteractiveParams, 0)
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

func mustMarshal(x any) json.RawMessage {
	data, err := json.Marshal(x)
	if err != nil {
		panic(err)
	}
	return json.RawMessage(data)
}

var implementInterfaceFormLazyEnum = []protocol.FormField{
	{
		Description: `fully qualified interface identifier path/to/pkg.interface; e.g., "net.Error"`,
		Type: protocol.FormFieldTypeLazyEnum{
			Kind:   "lazyEnum",
			Source: "workspaceSymbol",
			Config: mustMarshal(InteractiveWorkspaceSymbolEnumConfig{
				Kinds: []protocol.SymbolKind{protocol.Interface},
			}),
		},
		Default: "error",
	},
}

var implementInterfaceFormString = []protocol.FormField{
	{
		Description: `fully qualified interface identifier path/to/pkg.interface; e.g., "net.Error"`,
		Type: protocol.FormFieldTypeString{
			Kind: "string",
		},
		Default: "error",
	},
}

func resolveImplementInterface(options settings.ClientOptions, param *protocol.ExecuteCommandParams) error {
	var a0 command.ImplementInterfaceArgs
	if err := command.UnmarshalArgs(param.Arguments, &a0); err != nil {
		return err
	}

	var form []protocol.FormField
	if ok := options.SupportedInteractiveInputTypes[settings.InteractiveInputTypeLazyEnum]; ok {
		form = implementInterfaceFormLazyEnum
	} else if ok := options.SupportedInteractiveInputTypes[settings.InteractiveInputTypeString]; ok {
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

	v, err := FormAnswer[string](&param.InteractiveParams, 0)
	if err != nil {
		return err
	}

	// Gopls only validates the syntax of the string; it does not verify that
	// the package or interface actually exists in the workspace.
	validInterface := func(ifaceStr string) error {
		if ifaceStr == "error" {
			return nil
		}
		pkgPath, ifaceName, ok := morestrings.CutLast(ifaceStr, ".")
		if !ok {
			return fmt.Errorf(`invalid interface type name: want string of form "example.com/pkg.Type", got %q`, ifaceStr)
		}

		if err := module.CheckImportPath(pkgPath); err != nil {
			return fmt.Errorf("invalid package path %w", err)
		}

		if !token.IsIdentifier(ifaceName) {
			return fmt.Errorf("invalid type name: %q", ifaceName)
		}

		return nil
	}

	if err := validInterface(v); err != nil {
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

func FormAnswer[T any](params *protocol.InteractiveParams, index int) (v T, err error) {
	if len(params.FormAnswers) <= index {
		return v, fmt.Errorf("truncated FormAnswers: got %d items, want at least %d", len(params.FormAnswers), index+1)
	}

	v, ok := params.FormAnswers[index].(T)
	if !ok {
		return v, fmt.Errorf("invalid type at index %d, want %T: got %T", index, *new(T), params.FormAnswers[index])
	}

	return v, nil
}

// SanitizeTags cleans up comma-separated tags and ensures they are valid.
func SanitizeTags(tags string) (string, error) {
	parts := strings.Split(tags, ",")
	var clean []string

	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}

		// Use strings.ContainsFunc instead of a manual byte loop.
		// It returns true if any rune in the string matches the condition.
		if strings.ContainsFunc(p, func(r rune) bool {
			// Space, colon, quote, or any non-printable character (like control chars)
			return r == ' ' || r == ':' || r == '"' || !unicode.IsPrint(r)
		}) {
			return "", fmt.Errorf("illegal tag %q: cannot contain spaces, quotes, colons, or control characters", p)
		}

		clean = append(clean, p)
	}

	return strings.Join(clean, ","), nil
}
