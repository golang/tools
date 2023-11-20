// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package protocol

import "golang.org/x/tools/internal/diff"

// EditsFromDiffEdits converts diff.Edits to a non-nil slice of LSP TextEdits.
// See https://microsoft.github.io/language-server-protocol/specifications/lsp/3.17/specification/#textEditArray
func EditsFromDiffEdits(m *Mapper, edits []diff.Edit) ([]TextEdit, error) {
	// LSP doesn't require TextEditArray to be sorted:
	// this is the receiver's concern. But govim, and perhaps
	// other clients have historically relied on the order.
	edits = append([]diff.Edit(nil), edits...)
	diff.SortEdits(edits)

	result := make([]TextEdit, len(edits))
	for i, edit := range edits {
		rng, err := m.OffsetRange(edit.Start, edit.End)
		if err != nil {
			return nil, err
		}
		result[i] = TextEdit{
			Range:   rng,
			NewText: edit.New,
		}
	}
	return result, nil
}

// EditsToDiffEdits converts LSP TextEdits to diff.Edits.
// See https://microsoft.github.io/language-server-protocol/specifications/lsp/3.17/specification/#textEditArray
func EditsToDiffEdits(m *Mapper, edits []TextEdit) ([]diff.Edit, error) {
	if edits == nil {
		return nil, nil
	}
	result := make([]diff.Edit, len(edits))
	for i, edit := range edits {
		start, end, err := m.RangeOffsets(edit.Range)
		if err != nil {
			return nil, err
		}
		result[i] = diff.Edit{
			Start: start,
			End:   end,
			New:   edit.NewText,
		}
	}
	return result, nil
}

// ApplyEdits applies the patch (edits) to m.Content and returns the result.
// It also returns the edits converted to diff-package form.
func ApplyEdits(m *Mapper, edits []TextEdit) ([]byte, []diff.Edit, error) {
	diffEdits, err := EditsToDiffEdits(m, edits)
	if err != nil {
		return nil, nil, err
	}
	out, err := diff.ApplyBytes(m.Content, diffEdits)
	return out, diffEdits, err
}
