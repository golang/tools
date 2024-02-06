// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package protocol

import (
	"fmt"

	"golang.org/x/tools/internal/diff"
)

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

// AsTextEdits converts a slice possibly containing AnnotatedTextEdits
// to a slice of TextEdits.
func AsTextEdits(edits []Or_TextDocumentEdit_edits_Elem) []TextEdit {
	var result []TextEdit
	for _, e := range edits {
		var te TextEdit
		if x, ok := e.Value.(AnnotatedTextEdit); ok {
			te = x.TextEdit
		} else if x, ok := e.Value.(TextEdit); ok {
			te = x
		} else {
			panic(fmt.Sprintf("unexpected type %T, expected AnnotatedTextEdit or TextEdit", e.Value))
		}
		result = append(result, te)
	}
	return result
}

// AsAnnotatedTextEdits converts a slice of TextEdits
// to a slice of Or_TextDocumentEdit_edits_Elem.
// (returning a typed nil is required in server: in code_action.go and command.go))
func AsAnnotatedTextEdits(edits []TextEdit) []Or_TextDocumentEdit_edits_Elem {
	if edits == nil {
		return []Or_TextDocumentEdit_edits_Elem{}
	}
	var result []Or_TextDocumentEdit_edits_Elem
	for _, e := range edits {
		result = append(result, Or_TextDocumentEdit_edits_Elem{
			Value: TextEdit{
				Range:   e.Range,
				NewText: e.NewText,
			},
		})
	}
	return result
}

// TextEditsToDocumentChanges converts a set of edits within the
// specified (versioned) file to a singleton list of DocumentChanges
// (as required for a WorkspaceEdit).
func TextEditsToDocumentChanges(uri DocumentURI, version int32, edits []TextEdit) []DocumentChanges {
	return []DocumentChanges{{
		TextDocumentEdit: &TextDocumentEdit{
			TextDocument: OptionalVersionedTextDocumentIdentifier{
				Version:                version,
				TextDocumentIdentifier: TextDocumentIdentifier{URI: uri},
			},
			Edits: AsAnnotatedTextEdits(edits),
		},
	}}
}

// TextDocumentEditsToDocumentChanges wraps each TextDocumentEdit in a DocumentChange.
func TextDocumentEditsToDocumentChanges(edits []TextDocumentEdit) []DocumentChanges {
	changes := []DocumentChanges{} // non-nil
	for _, edit := range edits {
		edit := edit
		changes = append(changes, DocumentChanges{TextDocumentEdit: &edit})
	}
	return changes
}
