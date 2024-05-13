// Copyright 2022 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package protocol

import (
	"encoding/json"
	"fmt"
)

// DocumentChanges is a union of various file edit operations.
// At most one field of this struct is non-nil.
// (TODO(adonovan): rename to DocumentChange.)
//
// See https://microsoft.github.io/language-server-protocol/specifications/lsp/3.17/specification/#resourceChanges
type DocumentChanges struct {
	TextDocumentEdit *TextDocumentEdit
	CreateFile       *CreateFile
	RenameFile       *RenameFile
	DeleteFile       *DeleteFile
}

func (d *DocumentChanges) UnmarshalJSON(data []byte) error {
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		return err
	}

	if _, ok := m["textDocument"]; ok {
		d.TextDocumentEdit = new(TextDocumentEdit)
		return json.Unmarshal(data, d.TextDocumentEdit)
	}

	// The {Create,Rename,Delete}File types all share a 'kind' field.
	kind := m["kind"]
	switch kind {
	case "create":
		d.CreateFile = new(CreateFile)
		return json.Unmarshal(data, d.CreateFile)
	case "rename":
		d.RenameFile = new(RenameFile)
		return json.Unmarshal(data, d.RenameFile)
	case "delete":
		d.DeleteFile = new(DeleteFile)
		return json.Unmarshal(data, d.DeleteFile)
	}
	return fmt.Errorf("DocumentChanges: unexpected kind: %q", kind)
}

func (d *DocumentChanges) MarshalJSON() ([]byte, error) {
	if d.TextDocumentEdit != nil {
		return json.Marshal(d.TextDocumentEdit)
	} else if d.CreateFile != nil {
		return json.Marshal(d.CreateFile)
	} else if d.RenameFile != nil {
		return json.Marshal(d.RenameFile)
	} else if d.DeleteFile != nil {
		return json.Marshal(d.DeleteFile)
	}
	return nil, fmt.Errorf("empty DocumentChanges union value")
}
