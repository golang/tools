// Copyright 2019 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package diff supports a pluggable diff algorithm.
package diff

import (
	"bytes"
	"sort"

	"golang.org/x/tools/internal/span"
)

// TextEdit represents a change to a section of a document.
// The text within the specified span should be replaced by the supplied new text.
type TextEdit struct {
	Span    span.Span
	NewText string
}

// ComputeEdits is the type for a function that produces a set of edits that
// convert from the before content to the after content.
type ComputeEdits func(uri span.URI, before, after string) []TextEdit

// SortTextEdits attempts to order all edits by their starting points.
// The sort is stable so that edits with the same starting point will not
// be reordered.
func SortTextEdits(d []TextEdit) {
	// Use a stable sort to maintain the order of edits inserted at the same position.
	sort.SliceStable(d, func(i int, j int) bool {
		return span.Compare(d[i].Span, d[j].Span) < 0
	})
}

// ApplyEdits applies the set of edits to the before and returns the resulting
// content.
// It may panic or produce garbage if the edits are not valid for the provided
// before content.
func ApplyEdits(before string, edits []TextEdit) string {
	// Preconditions:
	//   - all of the edits apply to before
	//   - and all the spans for each TextEdit have the same URI

	// copy edits so we don't make a mess of the caller's slice
	s := make([]TextEdit, len(edits))
	copy(s, edits)
	edits = s

	// TODO(matloob): Initialize the Converter Once?
	var conv span.Converter = span.NewContentConverter("", []byte(before))
	offset := func(point span.Point) int {
		if point.HasOffset() {
			return point.Offset()
		}
		offset, err := conv.ToOffset(point.Line(), point.Column())
		if err != nil {
			panic(err)
		}
		return offset
	}

	// sort the copy
	sort.Slice(edits, func(i, j int) bool { return offset(edits[i].Span.Start()) < offset(edits[j].Span.Start()) })

	var after bytes.Buffer
	beforeOffset := 0
	for _, edit := range edits {
		if offset(edit.Span.Start()) < beforeOffset {
			panic("overlapping edits") // TODO(matloob): ApplyEdits doesn't return an error. What do we do?
		} else if offset(edit.Span.Start()) > beforeOffset {
			after.WriteString(before[beforeOffset:offset(edit.Span.Start())])
			beforeOffset = offset(edit.Span.Start())
		}
		// offset(edit.Span.Start) is now equal to beforeOffset
		after.WriteString(edit.NewText)
		beforeOffset += offset(edit.Span.End()) - offset(edit.Span.Start())
	}
	if beforeOffset < len(before) {
		after.WriteString(before[beforeOffset:])
		beforeOffset = len(before[beforeOffset:]) // just to preserve invariants
	}
	return after.String()
}
