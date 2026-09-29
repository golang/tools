// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package golang

import (
	"errors"
	"fmt"

	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/gopls/internal/protocol/command"
	"golang.org/x/tools/gopls/internal/settings"
)

// Dialog manages the collection, client capability negotiation, and validation
// of interactive form fields for a command.
//
// It reads client answers from params.FormAnswers and populates
// params.FormFields as the output parameter when [command.ErrPendingAnswer]
// is returned.
type Dialog struct {
	options settings.ClientOptions
	params  *protocol.InteractiveParams
	seen    map[string]int // number of times each question ID has been asked
	pending bool           // whether any answer is missing or failed validation
	err     error          // client protocol errors (malformed or duplicate answers)
}

// NewDialog returns a new [Dialog] for negotiating and validating interactive
// form fields with the client.
func NewDialog(options settings.ClientOptions, params *protocol.InteractiveParams) *Dialog {
	params.FormFields = nil
	return &Dialog{
		options: options,
		params:  params,
		seen:    make(map[string]int),
	}
}

// Ask adds the best client-supported candidate for q to the dialog form,
// validates and converts the user's answer (if provided), and returns the
// converted value. If the same question ID is asked multiple times, subsequent
// fields are suffixed with 1, 2, etc.
//
// If the answer is missing or fails validation, Ask marks the dialog as pending
// (and attaches any validation error to the field). If the answer is malformed,
// Ask records the client error to be returned by [Dialog.Check].
func (d *Dialog) Ask[In, Out any](q formQuestion[In, Out]) (res Out) {
	field := q.bestField(d.options)
	if n := d.seen[q.ID]; n > 0 {
		field.ID = fmt.Sprintf("%s%d", q.ID, n)
	}
	d.seen[q.ID]++
	defer func() {
		d.params.FormFields = append(d.params.FormFields, field)
	}()

	raw, exists, err := d.params.Answer[In](field.ID)
	if err != nil {
		d.err = errors.Join(d.err, err)
		return res
	}
	if !exists {
		d.pending = true
		return res
	}
	if q.convert == nil {
		return any(raw).(Out)
	}
	val, err := q.convert(raw)
	if err != nil {
		field.Error = err.Error()
		d.pending = true
		return res
	}
	return val
}

// Check reports whether all questions asked so far have valid answers.
// It returns any client protocol errors first, or [command.ErrPendingAnswer]
// if any question is missing an answer or failed validation.
func (d *Dialog) Check() error {
	if d.err != nil {
		return d.err
	}
	if d.pending {
		return command.ErrPendingAnswer
	}
	return nil
}

// formQuestion defines a question in an interactive dialog.
//
// In is the raw JSON answer type sent by the client (e.g., string), and Out is
// the validated and converted Go value returned to the caller.
type formQuestion[In, Out any] struct {
	ID          string
	Description string
	Required    bool
	Default     any
	// Types is a prioritized list of candidate field types (protocol.FormFieldType*),
	// ordered from most preferred to least preferred.
	Types   []any
	convert func(In) (Out, error)
}

// WithConvert returns a copy of q with the given convert function.
func (q formQuestion[In, Out]) WithConvert(fn func(In) (Out, error)) formQuestion[In, Out] {
	q.convert = fn
	return q
}

// bestField returns a [protocol.FormField] using the most preferred candidate
// type in q.Types that the client supports, or the zero FormField if the client
// supports none of them.
func (q formQuestion[In, Out]) bestField(options settings.ClientOptions) protocol.FormField {
	if len(q.Types) == 0 {
		panic(fmt.Sprintf("question %q has no candidate types", q.ID))
	}
	for _, typ := range q.Types {
		if options.SupportedInteractiveInputTypes[formFieldInputKind(typ)] {
			return protocol.FormField{
				ID:          q.ID,
				Description: q.Description,
				Type:        typ,
				Required:    q.Required,
				Default:     q.Default,
			}
		}
	}
	return protocol.FormField{}
}

// question erases the [In, Out] type parameters of [formQuestion] so questions
// with different output types can be grouped in a slice for [supportsDialog].
type question interface {
	bestField(settings.ClientOptions) protocol.FormField
}

// supportsDialog reports whether the client has sufficient protocol capabilities
// to participate in an interactive dialog for the given questions.
//
// The client must support at least one candidate input type for every question;
// otherwise supportsDialog returns false.
//
// If questions is empty or any question has no candidate types, supportsDialog panics.
func supportsDialog(options settings.ClientOptions, questions []question) bool {
	if len(questions) == 0 {
		panic("supportsDialog called with empty questions")
	}

	for _, q := range questions {
		if q.bestField(options) == (protocol.FormField{}) {
			return false // client supports none of the candidates
		}
	}

	return true
}

// formFieldInputKind extracts the interactive input type from a
// protocol.FormFieldType*.
//
// It panics if the type is unknown, as forms are generated internally by gopls.
func formFieldInputKind(typ any) protocol.FormFieldKind {
	switch t := typ.(type) {
	case protocol.FormFieldTypeString:
		return t.Kind
	case protocol.FormFieldTypeFile:
		return t.Kind
	case protocol.FormFieldTypeBool:
		return t.Kind
	case protocol.FormFieldTypeNumber:
		return t.Kind
	case protocol.FormFieldTypeEnum:
		return t.Kind
	case protocol.FormFieldTypeLazyEnum:
		return t.Kind
	case protocol.FormFieldTypeList:
		return t.Kind
	default:
		// A form field type was added to gopls without updating this function.
		panic(fmt.Sprintf("gopls bug: unhandled FormFieldType %T", typ))
	}
}
