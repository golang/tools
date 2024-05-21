// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package protocol

import (
	"encoding/json"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestInsertReplaceEdit_UnmarshalJSON(t *testing.T) {
	tests := []struct {
		name    string
		in      any
		wantErr bool
	}{
		{
			name: "TextEdit",
			in:   TextEdit{NewText: "new text", Range: Range{Start: Position{Line: 1}}},
		},
		{
			name: "InsertReplaceEdit",
			in:   InsertReplaceEdit{NewText: "new text", Insert: Range{Start: Position{Line: 100}}, Replace: Range{End: Position{Line: 200}}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := json.MarshalIndent(Or_CompletionItem_textEdit{Value: tt.in}, "", " ")
			if err != nil {
				t.Fatalf("failed to marshal: %v", err)
			}
			var decoded Or_CompletionItem_textEdit
			if err := json.Unmarshal(data, &decoded); err != nil {
				t.Fatalf("failed to unmarshal: %v", err)
			}
			if diff := cmp.Diff(tt.in, decoded.Value); diff != "" {
				t.Errorf("unmarshal returns unexpected result: (-want +got):\n%s", diff)
			}
		})
	}
}
