// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// This file defines types for an experimental feature (golang/go#76331,
// microsoft/language-server-protocol#1164). As an experimental feature,
// these types are not yet included in tsprotocol.go.

package protocol

import "encoding/json"

// FormFieldTypeString defines a text input.
//
// It is defined as a struct to allow for future extensibility, such as
// adding regex validation or file URI constraints.
type FormFieldTypeString struct {
	// Kind must be "string".
	Kind string `json:"kind"`
}

// FileExistence whether the file denoted by a DocumentURI exists.
//
// It is a bit set allowing combinations of existence states. For
// example, New|Existing allows either state.
type FileExistence uint32

const (
	// New indicates that file has not yet been created.
	FileExistenceNew FileExistence = 1 << 0
	// Existing indicates that the file exists already.
	FileExistenceExisting FileExistence = 1 << 1
)

// FileType represents the expected filesystem resource type.
//
// It is a bit set allowing combinations of file types. For example, Regular|Directory
// allows either types.
type FileType uint32

const (
	// Regular indicates the resource could be a regular file.
	FileTypeRegular FileType = 1 << 0
	// Directory indicates the resource could be a directory.
	FileTypeDirectory FileType = 1 << 1
)

// FormFieldTypeFile defines an input for a file or directory URI.
//
// The client determines the best mechanism to collect this information from
// the user (e.g., a graphical file picker, a text input with autocomplete, etc).
//
// The value returned by the client must be a valid "DocumentUri" as defined
// in the LSP specification:
// https://microsoft.github.io/language-server-protocol/specifications/lsp/3.17/specification/#documentUri
type FormFieldTypeFile struct {
	// Kind must be "file".
	Kind string `json:"kind"`

	// Existence constraint.
	Existence FileExistence `json:"existence"`

	// Type specifies the set of allowed file types (regular file, directory, etc).
	//
	// Only applicable against existing file.
	Type FileType `json:"type"`
}

// FormFieldTypeBool defines a boolean input.
type FormFieldTypeBool struct {
	// Kind must be "bool".
	Kind string `json:"kind"`
}

// FormFieldTypeNumber defines a numeric input.
//
// It is defined as a struct to allow for future extensibility, such as
// adding range constraints (min/max) or precision requirements.
type FormFieldTypeNumber struct {
	// Kind must be "number".
	Kind string `json:"kind"`
}

// FormEnumEntry represents a single option in an enumeration.
type FormEnumEntry struct {
	// Value is the unique string identifier for this option.
	//
	// This is the value that will be sent back to the server in
	// 'FormAnswers' if the user selects this option.
	Value string `json:"value"`

	// Description is the human-readable label presented to the user.
	Description string `json:"description"`
}

// FormFieldTypeEnum defines a selection from a set of values.
//
// Use this type when:
// - The number of options is small (e.g., < 20).
// - All options are known at the time the form is created.
type FormFieldTypeEnum struct {
	// Kind must be "enum".
	Kind string `json:"kind"`

	// Entries is the list of allowable options.
	Entries []FormEnumEntry `json:"entries"`
}

// FormFieldTypeLazyEnum defines a selection from a large or dynamic enum entry set.
//
// Use this type when:
//  1. The dataset is too large to send efficiently in a single payload
//     (e.g., thousands of workspace symbols, file uri or cloud resources).
//  2. The available options depend on the user's input (e.g., semantic search).
//  3. Generating the list is expensive and should only be done if requested.
//
// The client is expected to render a search interface (e.g., a combo box with
// a text input) and query the server via 'interactive/listEnum' as the user types.
type FormFieldTypeLazyEnum struct {
	// Kind must be "lazyEnum".
	Kind string `json:"kind"`

	// TODO(hxjiang): consider make debounce configurable since fetching
	// cloud resources could be expensive and slow.

	// Source identifies the data source on the server.
	//
	// Examples: "workspace/symbol", "database/schema", "git/tags".
	Source string `json:"source"`

	// Config contains the static settings for the source.
	// The client treats this as opaque data and echoes it back in the
	// 'interactive/listEnum' request.
	Config json.RawMessage `json:"config,omitempty"`
}

// FormFieldTypeList defines a homogeneous list of items.
type FormFieldTypeList struct {
	// Kind must be "list".
	Kind string `json:"kind"`

	// ElementType specifies the type of the items in the list.
	// It must be one of the FormFieldType* structs (e.g., FormFieldTypeString).
	ElementType any `json:"elementType"`
}

// FormField describes a single question in a form and its validation state.
type FormField struct {
	// Description is the text content of the question (the prompt) presented
	// to the user.
	Description string `json:"description"`

	// Type specifies the data type and validation constraints for the answer.
	//
	// It must be one of the FormFieldType* structs. The Kind field within the
	// struct determines the expected data type.
	//
	// The language client is expected to render an input appropriate for this
	// type. If the client does not support the specified type, it should
	// fall back to a string input.
	Type any `json:"type"`

	// Default specifies an optional initial value for the answer.
	//
	// If Type is FormFieldTypeEnum, this value must be present in the enum's
	// Values slice.
	Default any `json:"default,omitempty"`

	// Error provides a validation message from the language server.
	// If empty, the current answer is considered valid.
	Error string `json:"error,omitempty"`
}

// InteractiveParams facilitates a multi-step, interactive dialogue between the
// client and server during a Language Server Protocol (LSP) request.
//
// It implements a non-standard protocol extension microsoft/language-server-protocol#1164
// . By embedding this type into standard request parameters (such as
// [ExecuteCommandParams] or [RenameParams]) and pairing them with dedicated
// resolution methods (like [Server.ResolveCommand] or other ResolveXXX handlers),
// standard operations can be transformed into interactive workflows.
//
// Standard LSP resolution methods (like "codeAction/resolve") cannot be used
// for these interactive forms because editors often trigger them eagerly to
// render previews, which would prematurely present UI forms to the user.
// The dedicated ResolveXXX pattern ensures the interactive dialogue strictly
// begins only *after* the user has explicitly indicated intent (for example,
// by clicking a specific Code Action).
//
// The following sequence illustrates the typical handshake, using a code action
// that resolves to a command as an example:
//
//  1. The client requests code actions for the current text selection.
//  2. The server responds with a code action containing a standard LSP Command
//     (title, command, and arguments).
//  3. The client calls [Server.ResolveCommand] with the initial command details
//     wrapped in an [ExecuteCommandParams] to determine if the execution requires
//     interactive input.
//  4. The server responds with an [ExecuteCommandParams]. If user input is
//     required, the server populates the FormFields array with the required schema.
//  5. The client observes the non-empty FormFields and presents a corresponding
//     user interface.
//  6. The user submits their input, and the client issues another
//     [Server.ResolveCommand] request, this time populating the FormAnswers array.
//  7. The server validates the answers. If invalid, it returns a form with error
//     messages attached to specific FormFields. Steps 5-7 repeat until the server
//     omits FormFields entirely, indicating the answers are valid and complete.
//  8. The client calls [Server.ExecuteCommand] with the finalized FormAnswers to
//     execute the action.
//
// The server populates FormFields to define the input schema. If FormFields is
// omitted or empty, the interactive phase is considered complete and the provided
// FormAnswers have been fully validated.
//
// The server may optionally populate FormAnswers alongside FormFields to preserve
// previous user input or provide default values for the client to render.
type InteractiveParams struct {
	// FormFields defines the questions and validation errors in previous
	// answers to the same questions.
	//
	// This is a server-to-client field. The language server defines these, and
	// the client uses them to render the form.
	//
	// The interactive phase is considered complete when the server returns a
	// response where this slice is omitted.
	//
	// Note: This is a non-standard protocol extension. See microsoft/language-server-protocol#1164.
	FormFields []FormField `json:"formFields,omitempty"`

	// FormAnswers contains the values for the form questions.
	//
	// When sent by the language server, this field is optional but recommended
	// to support editing previous values.
	//
	// When sent by the language client as part of the ResolveXXX request, this
	// field is required. The slice must have the same length as FormFields (one
	// answer per question), where the answer at index i corresponds to the
	// field at index i.
	//
	// Note: This is a non-standard protocol extension. See microsoft/language-server-protocol#1164.
	FormAnswers []any `json:"formAnswers,omitempty"`
}

// InteractiveListEnumParams defines the parameters for the
// 'interactive/listEnum' request.
type InteractiveListEnumParams struct {
	// Source identifies the data source on the server.
	//
	// The client treats this as opaque data and echoes it back in the
	// 'interactive/listEnum' request.
	//
	// Examples: "workspace/symbol", "database/schema", "git/tags".
	Source string `json:"source"`

	// Config contains the static settings for the specified source.
	//
	// The client treats this as opaque data and echoes it back in the
	// 'interactive/listEnum' request.
	Config json.RawMessage `json:"config,omitempty"`

	// A query string to filter enum entries by.
	//
	// The exact interpretation of this string (e.g., fuzzy matching, exact
	// match, prefix search, or regular expression) is entirely up to the
	// server and may vary depending on the source. This follows the similar
	// semantics as the standard 'workspace/symbol' request. Clients may
	// send an empty string here to request a default set of enum entries.
	Query string `json:"query"`
}
