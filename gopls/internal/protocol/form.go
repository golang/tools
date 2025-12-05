// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// This file defines types for an experimental feature (golang/go#76331,
// microsoft/language-server-protocol#1164). As an experimental feature,
// these types are not yet included in tsprotocol.go.

package protocol

// FormFieldTypeString defines a text input.
//
// It is defined as a struct to allow for future extensibility, such as
// adding regex validation or file URI constraints.
type FormFieldTypeString struct {
	// Kind must be "string".
	Kind string `json:"kind"`
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

// FormFieldTypeEnum defines a selection from a set of values.
type FormFieldTypeEnum struct {
	// Kind must be "enum".
	Kind string `json:"kind"`

	// Name is an optional identifier for the enum type.
	Name string `json:"name,omitempty"`

	// Values is the set of allowable options.
	Values []string `json:"values"`

	// Description provides human-readable labels for the options.
	//
	// This slice must have the same length as Values, where Description[i]
	// corresponds to Values[i].
	Description []string `json:"description"`
}

// FormFieldTypeList defines a homogenous list of items.
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
