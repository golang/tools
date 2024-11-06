// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Code generated for LSP. DO NOT EDIT.

package protocol

// Code generated from protocol/metaModel.json at ref release/protocol/3.17.6-next.9 (hash c94395b5da53729e6dff931293b051009ccaaaa4).
// https://github.com/microsoft/vscode-languageserver-node/blob/release/protocol/3.17.6-next.9/protocol/metaModel.json
// LSP metaData.version = 3.17.0.

import "encoding/json"

import "fmt"

// UnmarshalError indicates that a JSON value did not conform to
// one of the expected cases of an LSP union type.
type UnmarshalError struct {
	msg string
}

func (e UnmarshalError) Error() string {
	return e.msg
}
func (t OrPLocation_workspace_symbol) MarshalJSON() ([]byte, error) {
	switch x := t.Value.(type) {
	case Location:
		return json.Marshal(x)
	case LocationUriOnly:
		return json.Marshal(x)
	case nil:
		return []byte("null"), nil
	}
	return nil, fmt.Errorf("type %T not one of [Location LocationUriOnly]", t)
}

func (t *OrPLocation_workspace_symbol) UnmarshalJSON(x []byte) error {
	if string(x) == "null" {
		t.Value = nil
		return nil
	}
	var h0 Location
	if err := json.Unmarshal(x, &h0); err == nil {
		t.Value = h0
		return nil
	}
	var h1 LocationUriOnly
	if err := json.Unmarshal(x, &h1); err == nil {
		t.Value = h1
		return nil
	}
	return &UnmarshalError{"unmarshal failed to match one of [Location LocationUriOnly]"}
}

func (t OrPSection_workspace_didChangeConfiguration) MarshalJSON() ([]byte, error) {
	switch x := t.Value.(type) {
	case []string:
		return json.Marshal(x)
	case string:
		return json.Marshal(x)
	case nil:
		return []byte("null"), nil
	}
	return nil, fmt.Errorf("type %T not one of [[]string string]", t)
}

func (t *OrPSection_workspace_didChangeConfiguration) UnmarshalJSON(x []byte) error {
	if string(x) == "null" {
		t.Value = nil
		return nil
	}
	var h0 []string
	if err := json.Unmarshal(x, &h0); err == nil {
		t.Value = h0
		return nil
	}
	var h1 string
	if err := json.Unmarshal(x, &h1); err == nil {
		t.Value = h1
		return nil
	}
	return &UnmarshalError{"unmarshal failed to match one of [[]string string]"}
}

func (t OrPTooltipPLabel) MarshalJSON() ([]byte, error) {
	switch x := t.Value.(type) {
	case MarkupContent:
		return json.Marshal(x)
	case string:
		return json.Marshal(x)
	case nil:
		return []byte("null"), nil
	}
	return nil, fmt.Errorf("type %T not one of [MarkupContent string]", t)
}

func (t *OrPTooltipPLabel) UnmarshalJSON(x []byte) error {
	if string(x) == "null" {
		t.Value = nil
		return nil
	}
	var h0 MarkupContent
	if err := json.Unmarshal(x, &h0); err == nil {
		t.Value = h0
		return nil
	}
	var h1 string
	if err := json.Unmarshal(x, &h1); err == nil {
		t.Value = h1
		return nil
	}
	return &UnmarshalError{"unmarshal failed to match one of [MarkupContent string]"}
}

func (t OrPTooltip_textDocument_inlayHint) MarshalJSON() ([]byte, error) {
	switch x := t.Value.(type) {
	case MarkupContent:
		return json.Marshal(x)
	case string:
		return json.Marshal(x)
	case nil:
		return []byte("null"), nil
	}
	return nil, fmt.Errorf("type %T not one of [MarkupContent string]", t)
}

func (t *OrPTooltip_textDocument_inlayHint) UnmarshalJSON(x []byte) error {
	if string(x) == "null" {
		t.Value = nil
		return nil
	}
	var h0 MarkupContent
	if err := json.Unmarshal(x, &h0); err == nil {
		t.Value = h0
		return nil
	}
	var h1 string
	if err := json.Unmarshal(x, &h1); err == nil {
		t.Value = h1
		return nil
	}
	return &UnmarshalError{"unmarshal failed to match one of [MarkupContent string]"}
}

func (t Or_CancelParams_id) MarshalJSON() ([]byte, error) {
	switch x := t.Value.(type) {
	case int32:
		return json.Marshal(x)
	case string:
		return json.Marshal(x)
	case nil:
		return []byte("null"), nil
	}
	return nil, fmt.Errorf("type %T not one of [int32 string]", t)
}

func (t *Or_CancelParams_id) UnmarshalJSON(x []byte) error {
	if string(x) == "null" {
		t.Value = nil
		return nil
	}
	var h0 int32
	if err := json.Unmarshal(x, &h0); err == nil {
		t.Value = h0
		return nil
	}
	var h1 string
	if err := json.Unmarshal(x, &h1); err == nil {
		t.Value = h1
		return nil
	}
	return &UnmarshalError{"unmarshal failed to match one of [int32 string]"}
}

func (t Or_ClientSemanticTokensRequestOptions_full) MarshalJSON() ([]byte, error) {
	switch x := t.Value.(type) {
	case ClientSemanticTokensRequestFullDelta:
		return json.Marshal(x)
	case bool:
		return json.Marshal(x)
	case nil:
		return []byte("null"), nil
	}
	return nil, fmt.Errorf("type %T not one of [ClientSemanticTokensRequestFullDelta bool]", t)
}

func (t *Or_ClientSemanticTokensRequestOptions_full) UnmarshalJSON(x []byte) error {
	if string(x) == "null" {
		t.Value = nil
		return nil
	}
	var h0 ClientSemanticTokensRequestFullDelta
	if err := json.Unmarshal(x, &h0); err == nil {
		t.Value = h0
		return nil
	}
	var h1 bool
	if err := json.Unmarshal(x, &h1); err == nil {
		t.Value = h1
		return nil
	}
	return &UnmarshalError{"unmarshal failed to match one of [ClientSemanticTokensRequestFullDelta bool]"}
}

func (t Or_ClientSemanticTokensRequestOptions_range) MarshalJSON() ([]byte, error) {
	switch x := t.Value.(type) {
	case Lit_ClientSemanticTokensRequestOptions_range_Item1:
		return json.Marshal(x)
	case bool:
		return json.Marshal(x)
	case nil:
		return []byte("null"), nil
	}
	return nil, fmt.Errorf("type %T not one of [Lit_ClientSemanticTokensRequestOptions_range_Item1 bool]", t)
}

func (t *Or_ClientSemanticTokensRequestOptions_range) UnmarshalJSON(x []byte) error {
	if string(x) == "null" {
		t.Value = nil
		return nil
	}
	var h0 Lit_ClientSemanticTokensRequestOptions_range_Item1
	if err := json.Unmarshal(x, &h0); err == nil {
		t.Value = h0
		return nil
	}
	var h1 bool
	if err := json.Unmarshal(x, &h1); err == nil {
		t.Value = h1
		return nil
	}
	return &UnmarshalError{"unmarshal failed to match one of [Lit_ClientSemanticTokensRequestOptions_range_Item1 bool]"}
}

func (t Or_CompletionItemDefaults_editRange) MarshalJSON() ([]byte, error) {
	switch x := t.Value.(type) {
	case EditRangeWithInsertReplace:
		return json.Marshal(x)
	case Range:
		return json.Marshal(x)
	case nil:
		return []byte("null"), nil
	}
	return nil, fmt.Errorf("type %T not one of [EditRangeWithInsertReplace Range]", t)
}

func (t *Or_CompletionItemDefaults_editRange) UnmarshalJSON(x []byte) error {
	if string(x) == "null" {
		t.Value = nil
		return nil
	}
	var h0 EditRangeWithInsertReplace
	if err := json.Unmarshal(x, &h0); err == nil {
		t.Value = h0
		return nil
	}
	var h1 Range
	if err := json.Unmarshal(x, &h1); err == nil {
		t.Value = h1
		return nil
	}
	return &UnmarshalError{"unmarshal failed to match one of [EditRangeWithInsertReplace Range]"}
}

func (t Or_CompletionItem_documentation) MarshalJSON() ([]byte, error) {
	switch x := t.Value.(type) {
	case MarkupContent:
		return json.Marshal(x)
	case string:
		return json.Marshal(x)
	case nil:
		return []byte("null"), nil
	}
	return nil, fmt.Errorf("type %T not one of [MarkupContent string]", t)
}

func (t *Or_CompletionItem_documentation) UnmarshalJSON(x []byte) error {
	if string(x) == "null" {
		t.Value = nil
		return nil
	}
	var h0 MarkupContent
	if err := json.Unmarshal(x, &h0); err == nil {
		t.Value = h0
		return nil
	}
	var h1 string
	if err := json.Unmarshal(x, &h1); err == nil {
		t.Value = h1
		return nil
	}
	return &UnmarshalError{"unmarshal failed to match one of [MarkupContent string]"}
}

func (t Or_CompletionItem_textEdit) MarshalJSON() ([]byte, error) {
	switch x := t.Value.(type) {
	case InsertReplaceEdit:
		return json.Marshal(x)
	case TextEdit:
		return json.Marshal(x)
	case nil:
		return []byte("null"), nil
	}
	return nil, fmt.Errorf("type %T not one of [InsertReplaceEdit TextEdit]", t)
}

func (t *Or_CompletionItem_textEdit) UnmarshalJSON(x []byte) error {
	if string(x) == "null" {
		t.Value = nil
		return nil
	}
	var h0 InsertReplaceEdit
	if err := json.Unmarshal(x, &h0); err == nil {
		t.Value = h0
		return nil
	}
	var h1 TextEdit
	if err := json.Unmarshal(x, &h1); err == nil {
		t.Value = h1
		return nil
	}
	return &UnmarshalError{"unmarshal failed to match one of [InsertReplaceEdit TextEdit]"}
}

func (t Or_Definition) MarshalJSON() ([]byte, error) {
	switch x := t.Value.(type) {
	case Location:
		return json.Marshal(x)
	case []Location:
		return json.Marshal(x)
	case nil:
		return []byte("null"), nil
	}
	return nil, fmt.Errorf("type %T not one of [Location []Location]", t)
}

func (t *Or_Definition) UnmarshalJSON(x []byte) error {
	if string(x) == "null" {
		t.Value = nil
		return nil
	}
	var h0 Location
	if err := json.Unmarshal(x, &h0); err == nil {
		t.Value = h0
		return nil
	}
	var h1 []Location
	if err := json.Unmarshal(x, &h1); err == nil {
		t.Value = h1
		return nil
	}
	return &UnmarshalError{"unmarshal failed to match one of [Location []Location]"}
}

func (t Or_Diagnostic_code) MarshalJSON() ([]byte, error) {
	switch x := t.Value.(type) {
	case int32:
		return json.Marshal(x)
	case string:
		return json.Marshal(x)
	case nil:
		return []byte("null"), nil
	}
	return nil, fmt.Errorf("type %T not one of [int32 string]", t)
}

func (t *Or_Diagnostic_code) UnmarshalJSON(x []byte) error {
	if string(x) == "null" {
		t.Value = nil
		return nil
	}
	var h0 int32
	if err := json.Unmarshal(x, &h0); err == nil {
		t.Value = h0
		return nil
	}
	var h1 string
	if err := json.Unmarshal(x, &h1); err == nil {
		t.Value = h1
		return nil
	}
	return &UnmarshalError{"unmarshal failed to match one of [int32 string]"}
}

func (t Or_DocumentDiagnosticReport) MarshalJSON() ([]byte, error) {
	switch x := t.Value.(type) {
	case RelatedFullDocumentDiagnosticReport:
		return json.Marshal(x)
	case RelatedUnchangedDocumentDiagnosticReport:
		return json.Marshal(x)
	case nil:
		return []byte("null"), nil
	}
	return nil, fmt.Errorf("type %T not one of [RelatedFullDocumentDiagnosticReport RelatedUnchangedDocumentDiagnosticReport]", t)
}

func (t *Or_DocumentDiagnosticReport) UnmarshalJSON(x []byte) error {
	if string(x) == "null" {
		t.Value = nil
		return nil
	}
	var h0 RelatedFullDocumentDiagnosticReport
	if err := json.Unmarshal(x, &h0); err == nil {
		t.Value = h0
		return nil
	}
	var h1 RelatedUnchangedDocumentDiagnosticReport
	if err := json.Unmarshal(x, &h1); err == nil {
		t.Value = h1
		return nil
	}
	return &UnmarshalError{"unmarshal failed to match one of [RelatedFullDocumentDiagnosticReport RelatedUnchangedDocumentDiagnosticReport]"}
}

func (t Or_DocumentDiagnosticReportPartialResult_relatedDocuments_Value) MarshalJSON() ([]byte, error) {
	switch x := t.Value.(type) {
	case FullDocumentDiagnosticReport:
		return json.Marshal(x)
	case UnchangedDocumentDiagnosticReport:
		return json.Marshal(x)
	case nil:
		return []byte("null"), nil
	}
	return nil, fmt.Errorf("type %T not one of [FullDocumentDiagnosticReport UnchangedDocumentDiagnosticReport]", t)
}

func (t *Or_DocumentDiagnosticReportPartialResult_relatedDocuments_Value) UnmarshalJSON(x []byte) error {
	if string(x) == "null" {
		t.Value = nil
		return nil
	}
	var h0 FullDocumentDiagnosticReport
	if err := json.Unmarshal(x, &h0); err == nil {
		t.Value = h0
		return nil
	}
	var h1 UnchangedDocumentDiagnosticReport
	if err := json.Unmarshal(x, &h1); err == nil {
		t.Value = h1
		return nil
	}
	return &UnmarshalError{"unmarshal failed to match one of [FullDocumentDiagnosticReport UnchangedDocumentDiagnosticReport]"}
}

func (t Or_DocumentFilter) MarshalJSON() ([]byte, error) {
	switch x := t.Value.(type) {
	case NotebookCellTextDocumentFilter:
		return json.Marshal(x)
	case TextDocumentFilter:
		return json.Marshal(x)
	case nil:
		return []byte("null"), nil
	}
	return nil, fmt.Errorf("type %T not one of [NotebookCellTextDocumentFilter TextDocumentFilter]", t)
}

func (t *Or_DocumentFilter) UnmarshalJSON(x []byte) error {
	if string(x) == "null" {
		t.Value = nil
		return nil
	}
	var h0 NotebookCellTextDocumentFilter
	if err := json.Unmarshal(x, &h0); err == nil {
		t.Value = h0
		return nil
	}
	var h1 TextDocumentFilter
	if err := json.Unmarshal(x, &h1); err == nil {
		t.Value = h1
		return nil
	}
	return &UnmarshalError{"unmarshal failed to match one of [NotebookCellTextDocumentFilter TextDocumentFilter]"}
}

func (t Or_GlobPattern) MarshalJSON() ([]byte, error) {
	switch x := t.Value.(type) {
	case Pattern:
		return json.Marshal(x)
	case RelativePattern:
		return json.Marshal(x)
	case nil:
		return []byte("null"), nil
	}
	return nil, fmt.Errorf("type %T not one of [Pattern RelativePattern]", t)
}

func (t *Or_GlobPattern) UnmarshalJSON(x []byte) error {
	if string(x) == "null" {
		t.Value = nil
		return nil
	}
	var h0 Pattern
	if err := json.Unmarshal(x, &h0); err == nil {
		t.Value = h0
		return nil
	}
	var h1 RelativePattern
	if err := json.Unmarshal(x, &h1); err == nil {
		t.Value = h1
		return nil
	}
	return &UnmarshalError{"unmarshal failed to match one of [Pattern RelativePattern]"}
}

func (t Or_Hover_contents) MarshalJSON() ([]byte, error) {
	switch x := t.Value.(type) {
	case MarkedString:
		return json.Marshal(x)
	case MarkupContent:
		return json.Marshal(x)
	case []MarkedString:
		return json.Marshal(x)
	case nil:
		return []byte("null"), nil
	}
	return nil, fmt.Errorf("type %T not one of [MarkedString MarkupContent []MarkedString]", t)
}

func (t *Or_Hover_contents) UnmarshalJSON(x []byte) error {
	if string(x) == "null" {
		t.Value = nil
		return nil
	}
	var h0 MarkedString
	if err := json.Unmarshal(x, &h0); err == nil {
		t.Value = h0
		return nil
	}
	var h1 MarkupContent
	if err := json.Unmarshal(x, &h1); err == nil {
		t.Value = h1
		return nil
	}
	var h2 []MarkedString
	if err := json.Unmarshal(x, &h2); err == nil {
		t.Value = h2
		return nil
	}
	return &UnmarshalError{"unmarshal failed to match one of [MarkedString MarkupContent []MarkedString]"}
}

func (t Or_InlayHint_label) MarshalJSON() ([]byte, error) {
	switch x := t.Value.(type) {
	case []InlayHintLabelPart:
		return json.Marshal(x)
	case string:
		return json.Marshal(x)
	case nil:
		return []byte("null"), nil
	}
	return nil, fmt.Errorf("type %T not one of [[]InlayHintLabelPart string]", t)
}

func (t *Or_InlayHint_label) UnmarshalJSON(x []byte) error {
	if string(x) == "null" {
		t.Value = nil
		return nil
	}
	var h0 []InlayHintLabelPart
	if err := json.Unmarshal(x, &h0); err == nil {
		t.Value = h0
		return nil
	}
	var h1 string
	if err := json.Unmarshal(x, &h1); err == nil {
		t.Value = h1
		return nil
	}
	return &UnmarshalError{"unmarshal failed to match one of [[]InlayHintLabelPart string]"}
}

func (t Or_InlineCompletionItem_insertText) MarshalJSON() ([]byte, error) {
	switch x := t.Value.(type) {
	case StringValue:
		return json.Marshal(x)
	case string:
		return json.Marshal(x)
	case nil:
		return []byte("null"), nil
	}
	return nil, fmt.Errorf("type %T not one of [StringValue string]", t)
}

func (t *Or_InlineCompletionItem_insertText) UnmarshalJSON(x []byte) error {
	if string(x) == "null" {
		t.Value = nil
		return nil
	}
	var h0 StringValue
	if err := json.Unmarshal(x, &h0); err == nil {
		t.Value = h0
		return nil
	}
	var h1 string
	if err := json.Unmarshal(x, &h1); err == nil {
		t.Value = h1
		return nil
	}
	return &UnmarshalError{"unmarshal failed to match one of [StringValue string]"}
}

func (t Or_InlineValue) MarshalJSON() ([]byte, error) {
	switch x := t.Value.(type) {
	case InlineValueEvaluatableExpression:
		return json.Marshal(x)
	case InlineValueText:
		return json.Marshal(x)
	case InlineValueVariableLookup:
		return json.Marshal(x)
	case nil:
		return []byte("null"), nil
	}
	return nil, fmt.Errorf("type %T not one of [InlineValueEvaluatableExpression InlineValueText InlineValueVariableLookup]", t)
}

func (t *Or_InlineValue) UnmarshalJSON(x []byte) error {
	if string(x) == "null" {
		t.Value = nil
		return nil
	}
	var h0 InlineValueEvaluatableExpression
	if err := json.Unmarshal(x, &h0); err == nil {
		t.Value = h0
		return nil
	}
	var h1 InlineValueText
	if err := json.Unmarshal(x, &h1); err == nil {
		t.Value = h1
		return nil
	}
	var h2 InlineValueVariableLookup
	if err := json.Unmarshal(x, &h2); err == nil {
		t.Value = h2
		return nil
	}
	return &UnmarshalError{"unmarshal failed to match one of [InlineValueEvaluatableExpression InlineValueText InlineValueVariableLookup]"}
}

func (t Or_MarkedString) MarshalJSON() ([]byte, error) {
	switch x := t.Value.(type) {
	case MarkedStringWithLanguage:
		return json.Marshal(x)
	case string:
		return json.Marshal(x)
	case nil:
		return []byte("null"), nil
	}
	return nil, fmt.Errorf("type %T not one of [MarkedStringWithLanguage string]", t)
}

func (t *Or_MarkedString) UnmarshalJSON(x []byte) error {
	if string(x) == "null" {
		t.Value = nil
		return nil
	}
	var h0 MarkedStringWithLanguage
	if err := json.Unmarshal(x, &h0); err == nil {
		t.Value = h0
		return nil
	}
	var h1 string
	if err := json.Unmarshal(x, &h1); err == nil {
		t.Value = h1
		return nil
	}
	return &UnmarshalError{"unmarshal failed to match one of [MarkedStringWithLanguage string]"}
}

func (t Or_NotebookCellTextDocumentFilter_notebook) MarshalJSON() ([]byte, error) {
	switch x := t.Value.(type) {
	case NotebookDocumentFilter:
		return json.Marshal(x)
	case string:
		return json.Marshal(x)
	case nil:
		return []byte("null"), nil
	}
	return nil, fmt.Errorf("type %T not one of [NotebookDocumentFilter string]", t)
}

func (t *Or_NotebookCellTextDocumentFilter_notebook) UnmarshalJSON(x []byte) error {
	if string(x) == "null" {
		t.Value = nil
		return nil
	}
	var h0 NotebookDocumentFilter
	if err := json.Unmarshal(x, &h0); err == nil {
		t.Value = h0
		return nil
	}
	var h1 string
	if err := json.Unmarshal(x, &h1); err == nil {
		t.Value = h1
		return nil
	}
	return &UnmarshalError{"unmarshal failed to match one of [NotebookDocumentFilter string]"}
}

func (t Or_NotebookDocumentFilter) MarshalJSON() ([]byte, error) {
	switch x := t.Value.(type) {
	case NotebookDocumentFilterNotebookType:
		return json.Marshal(x)
	case NotebookDocumentFilterPattern:
		return json.Marshal(x)
	case NotebookDocumentFilterScheme:
		return json.Marshal(x)
	case nil:
		return []byte("null"), nil
	}
	return nil, fmt.Errorf("type %T not one of [NotebookDocumentFilterNotebookType NotebookDocumentFilterPattern NotebookDocumentFilterScheme]", t)
}

func (t *Or_NotebookDocumentFilter) UnmarshalJSON(x []byte) error {
	if string(x) == "null" {
		t.Value = nil
		return nil
	}
	var h0 NotebookDocumentFilterNotebookType
	if err := json.Unmarshal(x, &h0); err == nil {
		t.Value = h0
		return nil
	}
	var h1 NotebookDocumentFilterPattern
	if err := json.Unmarshal(x, &h1); err == nil {
		t.Value = h1
		return nil
	}
	var h2 NotebookDocumentFilterScheme
	if err := json.Unmarshal(x, &h2); err == nil {
		t.Value = h2
		return nil
	}
	return &UnmarshalError{"unmarshal failed to match one of [NotebookDocumentFilterNotebookType NotebookDocumentFilterPattern NotebookDocumentFilterScheme]"}
}

func (t Or_NotebookDocumentFilterWithCells_notebook) MarshalJSON() ([]byte, error) {
	switch x := t.Value.(type) {
	case NotebookDocumentFilter:
		return json.Marshal(x)
	case string:
		return json.Marshal(x)
	case nil:
		return []byte("null"), nil
	}
	return nil, fmt.Errorf("type %T not one of [NotebookDocumentFilter string]", t)
}

func (t *Or_NotebookDocumentFilterWithCells_notebook) UnmarshalJSON(x []byte) error {
	if string(x) == "null" {
		t.Value = nil
		return nil
	}
	var h0 NotebookDocumentFilter
	if err := json.Unmarshal(x, &h0); err == nil {
		t.Value = h0
		return nil
	}
	var h1 string
	if err := json.Unmarshal(x, &h1); err == nil {
		t.Value = h1
		return nil
	}
	return &UnmarshalError{"unmarshal failed to match one of [NotebookDocumentFilter string]"}
}

func (t Or_NotebookDocumentFilterWithNotebook_notebook) MarshalJSON() ([]byte, error) {
	switch x := t.Value.(type) {
	case NotebookDocumentFilter:
		return json.Marshal(x)
	case string:
		return json.Marshal(x)
	case nil:
		return []byte("null"), nil
	}
	return nil, fmt.Errorf("type %T not one of [NotebookDocumentFilter string]", t)
}

func (t *Or_NotebookDocumentFilterWithNotebook_notebook) UnmarshalJSON(x []byte) error {
	if string(x) == "null" {
		t.Value = nil
		return nil
	}
	var h0 NotebookDocumentFilter
	if err := json.Unmarshal(x, &h0); err == nil {
		t.Value = h0
		return nil
	}
	var h1 string
	if err := json.Unmarshal(x, &h1); err == nil {
		t.Value = h1
		return nil
	}
	return &UnmarshalError{"unmarshal failed to match one of [NotebookDocumentFilter string]"}
}

func (t Or_NotebookDocumentSyncOptions_notebookSelector_Elem) MarshalJSON() ([]byte, error) {
	switch x := t.Value.(type) {
	case NotebookDocumentFilterWithCells:
		return json.Marshal(x)
	case NotebookDocumentFilterWithNotebook:
		return json.Marshal(x)
	case nil:
		return []byte("null"), nil
	}
	return nil, fmt.Errorf("type %T not one of [NotebookDocumentFilterWithCells NotebookDocumentFilterWithNotebook]", t)
}

func (t *Or_NotebookDocumentSyncOptions_notebookSelector_Elem) UnmarshalJSON(x []byte) error {
	if string(x) == "null" {
		t.Value = nil
		return nil
	}
	var h0 NotebookDocumentFilterWithCells
	if err := json.Unmarshal(x, &h0); err == nil {
		t.Value = h0
		return nil
	}
	var h1 NotebookDocumentFilterWithNotebook
	if err := json.Unmarshal(x, &h1); err == nil {
		t.Value = h1
		return nil
	}
	return &UnmarshalError{"unmarshal failed to match one of [NotebookDocumentFilterWithCells NotebookDocumentFilterWithNotebook]"}
}

func (t Or_RelatedFullDocumentDiagnosticReport_relatedDocuments_Value) MarshalJSON() ([]byte, error) {
	switch x := t.Value.(type) {
	case FullDocumentDiagnosticReport:
		return json.Marshal(x)
	case UnchangedDocumentDiagnosticReport:
		return json.Marshal(x)
	case nil:
		return []byte("null"), nil
	}
	return nil, fmt.Errorf("type %T not one of [FullDocumentDiagnosticReport UnchangedDocumentDiagnosticReport]", t)
}

func (t *Or_RelatedFullDocumentDiagnosticReport_relatedDocuments_Value) UnmarshalJSON(x []byte) error {
	if string(x) == "null" {
		t.Value = nil
		return nil
	}
	var h0 FullDocumentDiagnosticReport
	if err := json.Unmarshal(x, &h0); err == nil {
		t.Value = h0
		return nil
	}
	var h1 UnchangedDocumentDiagnosticReport
	if err := json.Unmarshal(x, &h1); err == nil {
		t.Value = h1
		return nil
	}
	return &UnmarshalError{"unmarshal failed to match one of [FullDocumentDiagnosticReport UnchangedDocumentDiagnosticReport]"}
}

func (t Or_RelatedUnchangedDocumentDiagnosticReport_relatedDocuments_Value) MarshalJSON() ([]byte, error) {
	switch x := t.Value.(type) {
	case FullDocumentDiagnosticReport:
		return json.Marshal(x)
	case UnchangedDocumentDiagnosticReport:
		return json.Marshal(x)
	case nil:
		return []byte("null"), nil
	}
	return nil, fmt.Errorf("type %T not one of [FullDocumentDiagnosticReport UnchangedDocumentDiagnosticReport]", t)
}

func (t *Or_RelatedUnchangedDocumentDiagnosticReport_relatedDocuments_Value) UnmarshalJSON(x []byte) error {
	if string(x) == "null" {
		t.Value = nil
		return nil
	}
	var h0 FullDocumentDiagnosticReport
	if err := json.Unmarshal(x, &h0); err == nil {
		t.Value = h0
		return nil
	}
	var h1 UnchangedDocumentDiagnosticReport
	if err := json.Unmarshal(x, &h1); err == nil {
		t.Value = h1
		return nil
	}
	return &UnmarshalError{"unmarshal failed to match one of [FullDocumentDiagnosticReport UnchangedDocumentDiagnosticReport]"}
}

func (t Or_Result_textDocument_codeAction_Item0_Elem) MarshalJSON() ([]byte, error) {
	switch x := t.Value.(type) {
	case CodeAction:
		return json.Marshal(x)
	case Command:
		return json.Marshal(x)
	case nil:
		return []byte("null"), nil
	}
	return nil, fmt.Errorf("type %T not one of [CodeAction Command]", t)
}

func (t *Or_Result_textDocument_codeAction_Item0_Elem) UnmarshalJSON(x []byte) error {
	if string(x) == "null" {
		t.Value = nil
		return nil
	}
	var h0 CodeAction
	if err := json.Unmarshal(x, &h0); err == nil {
		t.Value = h0
		return nil
	}
	var h1 Command
	if err := json.Unmarshal(x, &h1); err == nil {
		t.Value = h1
		return nil
	}
	return &UnmarshalError{"unmarshal failed to match one of [CodeAction Command]"}
}

func (t Or_Result_textDocument_inlineCompletion) MarshalJSON() ([]byte, error) {
	switch x := t.Value.(type) {
	case InlineCompletionList:
		return json.Marshal(x)
	case []InlineCompletionItem:
		return json.Marshal(x)
	case nil:
		return []byte("null"), nil
	}
	return nil, fmt.Errorf("type %T not one of [InlineCompletionList []InlineCompletionItem]", t)
}

func (t *Or_Result_textDocument_inlineCompletion) UnmarshalJSON(x []byte) error {
	if string(x) == "null" {
		t.Value = nil
		return nil
	}
	var h0 InlineCompletionList
	if err := json.Unmarshal(x, &h0); err == nil {
		t.Value = h0
		return nil
	}
	var h1 []InlineCompletionItem
	if err := json.Unmarshal(x, &h1); err == nil {
		t.Value = h1
		return nil
	}
	return &UnmarshalError{"unmarshal failed to match one of [InlineCompletionList []InlineCompletionItem]"}
}

func (t Or_SemanticTokensOptions_full) MarshalJSON() ([]byte, error) {
	switch x := t.Value.(type) {
	case SemanticTokensFullDelta:
		return json.Marshal(x)
	case bool:
		return json.Marshal(x)
	case nil:
		return []byte("null"), nil
	}
	return nil, fmt.Errorf("type %T not one of [SemanticTokensFullDelta bool]", t)
}

func (t *Or_SemanticTokensOptions_full) UnmarshalJSON(x []byte) error {
	if string(x) == "null" {
		t.Value = nil
		return nil
	}
	var h0 SemanticTokensFullDelta
	if err := json.Unmarshal(x, &h0); err == nil {
		t.Value = h0
		return nil
	}
	var h1 bool
	if err := json.Unmarshal(x, &h1); err == nil {
		t.Value = h1
		return nil
	}
	return &UnmarshalError{"unmarshal failed to match one of [SemanticTokensFullDelta bool]"}
}

func (t Or_SemanticTokensOptions_range) MarshalJSON() ([]byte, error) {
	switch x := t.Value.(type) {
	case PRangeESemanticTokensOptions:
		return json.Marshal(x)
	case bool:
		return json.Marshal(x)
	case nil:
		return []byte("null"), nil
	}
	return nil, fmt.Errorf("type %T not one of [PRangeESemanticTokensOptions bool]", t)
}

func (t *Or_SemanticTokensOptions_range) UnmarshalJSON(x []byte) error {
	if string(x) == "null" {
		t.Value = nil
		return nil
	}
	var h0 PRangeESemanticTokensOptions
	if err := json.Unmarshal(x, &h0); err == nil {
		t.Value = h0
		return nil
	}
	var h1 bool
	if err := json.Unmarshal(x, &h1); err == nil {
		t.Value = h1
		return nil
	}
	return &UnmarshalError{"unmarshal failed to match one of [PRangeESemanticTokensOptions bool]"}
}

func (t Or_ServerCapabilities_callHierarchyProvider) MarshalJSON() ([]byte, error) {
	switch x := t.Value.(type) {
	case CallHierarchyOptions:
		return json.Marshal(x)
	case CallHierarchyRegistrationOptions:
		return json.Marshal(x)
	case bool:
		return json.Marshal(x)
	case nil:
		return []byte("null"), nil
	}
	return nil, fmt.Errorf("type %T not one of [CallHierarchyOptions CallHierarchyRegistrationOptions bool]", t)
}

func (t *Or_ServerCapabilities_callHierarchyProvider) UnmarshalJSON(x []byte) error {
	if string(x) == "null" {
		t.Value = nil
		return nil
	}
	var h0 CallHierarchyOptions
	if err := json.Unmarshal(x, &h0); err == nil {
		t.Value = h0
		return nil
	}
	var h1 CallHierarchyRegistrationOptions
	if err := json.Unmarshal(x, &h1); err == nil {
		t.Value = h1
		return nil
	}
	var h2 bool
	if err := json.Unmarshal(x, &h2); err == nil {
		t.Value = h2
		return nil
	}
	return &UnmarshalError{"unmarshal failed to match one of [CallHierarchyOptions CallHierarchyRegistrationOptions bool]"}
}

func (t Or_ServerCapabilities_codeActionProvider) MarshalJSON() ([]byte, error) {
	switch x := t.Value.(type) {
	case CodeActionOptions:
		return json.Marshal(x)
	case bool:
		return json.Marshal(x)
	case nil:
		return []byte("null"), nil
	}
	return nil, fmt.Errorf("type %T not one of [CodeActionOptions bool]", t)
}

func (t *Or_ServerCapabilities_codeActionProvider) UnmarshalJSON(x []byte) error {
	if string(x) == "null" {
		t.Value = nil
		return nil
	}
	var h0 CodeActionOptions
	if err := json.Unmarshal(x, &h0); err == nil {
		t.Value = h0
		return nil
	}
	var h1 bool
	if err := json.Unmarshal(x, &h1); err == nil {
		t.Value = h1
		return nil
	}
	return &UnmarshalError{"unmarshal failed to match one of [CodeActionOptions bool]"}
}

func (t Or_ServerCapabilities_colorProvider) MarshalJSON() ([]byte, error) {
	switch x := t.Value.(type) {
	case DocumentColorOptions:
		return json.Marshal(x)
	case DocumentColorRegistrationOptions:
		return json.Marshal(x)
	case bool:
		return json.Marshal(x)
	case nil:
		return []byte("null"), nil
	}
	return nil, fmt.Errorf("type %T not one of [DocumentColorOptions DocumentColorRegistrationOptions bool]", t)
}

func (t *Or_ServerCapabilities_colorProvider) UnmarshalJSON(x []byte) error {
	if string(x) == "null" {
		t.Value = nil
		return nil
	}
	var h0 DocumentColorOptions
	if err := json.Unmarshal(x, &h0); err == nil {
		t.Value = h0
		return nil
	}
	var h1 DocumentColorRegistrationOptions
	if err := json.Unmarshal(x, &h1); err == nil {
		t.Value = h1
		return nil
	}
	var h2 bool
	if err := json.Unmarshal(x, &h2); err == nil {
		t.Value = h2
		return nil
	}
	return &UnmarshalError{"unmarshal failed to match one of [DocumentColorOptions DocumentColorRegistrationOptions bool]"}
}

func (t Or_ServerCapabilities_declarationProvider) MarshalJSON() ([]byte, error) {
	switch x := t.Value.(type) {
	case DeclarationOptions:
		return json.Marshal(x)
	case DeclarationRegistrationOptions:
		return json.Marshal(x)
	case bool:
		return json.Marshal(x)
	case nil:
		return []byte("null"), nil
	}
	return nil, fmt.Errorf("type %T not one of [DeclarationOptions DeclarationRegistrationOptions bool]", t)
}

func (t *Or_ServerCapabilities_declarationProvider) UnmarshalJSON(x []byte) error {
	if string(x) == "null" {
		t.Value = nil
		return nil
	}
	var h0 DeclarationOptions
	if err := json.Unmarshal(x, &h0); err == nil {
		t.Value = h0
		return nil
	}
	var h1 DeclarationRegistrationOptions
	if err := json.Unmarshal(x, &h1); err == nil {
		t.Value = h1
		return nil
	}
	var h2 bool
	if err := json.Unmarshal(x, &h2); err == nil {
		t.Value = h2
		return nil
	}
	return &UnmarshalError{"unmarshal failed to match one of [DeclarationOptions DeclarationRegistrationOptions bool]"}
}

func (t Or_ServerCapabilities_definitionProvider) MarshalJSON() ([]byte, error) {
	switch x := t.Value.(type) {
	case DefinitionOptions:
		return json.Marshal(x)
	case bool:
		return json.Marshal(x)
	case nil:
		return []byte("null"), nil
	}
	return nil, fmt.Errorf("type %T not one of [DefinitionOptions bool]", t)
}

func (t *Or_ServerCapabilities_definitionProvider) UnmarshalJSON(x []byte) error {
	if string(x) == "null" {
		t.Value = nil
		return nil
	}
	var h0 DefinitionOptions
	if err := json.Unmarshal(x, &h0); err == nil {
		t.Value = h0
		return nil
	}
	var h1 bool
	if err := json.Unmarshal(x, &h1); err == nil {
		t.Value = h1
		return nil
	}
	return &UnmarshalError{"unmarshal failed to match one of [DefinitionOptions bool]"}
}

func (t Or_ServerCapabilities_diagnosticProvider) MarshalJSON() ([]byte, error) {
	switch x := t.Value.(type) {
	case DiagnosticOptions:
		return json.Marshal(x)
	case DiagnosticRegistrationOptions:
		return json.Marshal(x)
	case nil:
		return []byte("null"), nil
	}
	return nil, fmt.Errorf("type %T not one of [DiagnosticOptions DiagnosticRegistrationOptions]", t)
}

func (t *Or_ServerCapabilities_diagnosticProvider) UnmarshalJSON(x []byte) error {
	if string(x) == "null" {
		t.Value = nil
		return nil
	}
	var h0 DiagnosticOptions
	if err := json.Unmarshal(x, &h0); err == nil {
		t.Value = h0
		return nil
	}
	var h1 DiagnosticRegistrationOptions
	if err := json.Unmarshal(x, &h1); err == nil {
		t.Value = h1
		return nil
	}
	return &UnmarshalError{"unmarshal failed to match one of [DiagnosticOptions DiagnosticRegistrationOptions]"}
}

func (t Or_ServerCapabilities_documentFormattingProvider) MarshalJSON() ([]byte, error) {
	switch x := t.Value.(type) {
	case DocumentFormattingOptions:
		return json.Marshal(x)
	case bool:
		return json.Marshal(x)
	case nil:
		return []byte("null"), nil
	}
	return nil, fmt.Errorf("type %T not one of [DocumentFormattingOptions bool]", t)
}

func (t *Or_ServerCapabilities_documentFormattingProvider) UnmarshalJSON(x []byte) error {
	if string(x) == "null" {
		t.Value = nil
		return nil
	}
	var h0 DocumentFormattingOptions
	if err := json.Unmarshal(x, &h0); err == nil {
		t.Value = h0
		return nil
	}
	var h1 bool
	if err := json.Unmarshal(x, &h1); err == nil {
		t.Value = h1
		return nil
	}
	return &UnmarshalError{"unmarshal failed to match one of [DocumentFormattingOptions bool]"}
}

func (t Or_ServerCapabilities_documentHighlightProvider) MarshalJSON() ([]byte, error) {
	switch x := t.Value.(type) {
	case DocumentHighlightOptions:
		return json.Marshal(x)
	case bool:
		return json.Marshal(x)
	case nil:
		return []byte("null"), nil
	}
	return nil, fmt.Errorf("type %T not one of [DocumentHighlightOptions bool]", t)
}

func (t *Or_ServerCapabilities_documentHighlightProvider) UnmarshalJSON(x []byte) error {
	if string(x) == "null" {
		t.Value = nil
		return nil
	}
	var h0 DocumentHighlightOptions
	if err := json.Unmarshal(x, &h0); err == nil {
		t.Value = h0
		return nil
	}
	var h1 bool
	if err := json.Unmarshal(x, &h1); err == nil {
		t.Value = h1
		return nil
	}
	return &UnmarshalError{"unmarshal failed to match one of [DocumentHighlightOptions bool]"}
}

func (t Or_ServerCapabilities_documentRangeFormattingProvider) MarshalJSON() ([]byte, error) {
	switch x := t.Value.(type) {
	case DocumentRangeFormattingOptions:
		return json.Marshal(x)
	case bool:
		return json.Marshal(x)
	case nil:
		return []byte("null"), nil
	}
	return nil, fmt.Errorf("type %T not one of [DocumentRangeFormattingOptions bool]", t)
}

func (t *Or_ServerCapabilities_documentRangeFormattingProvider) UnmarshalJSON(x []byte) error {
	if string(x) == "null" {
		t.Value = nil
		return nil
	}
	var h0 DocumentRangeFormattingOptions
	if err := json.Unmarshal(x, &h0); err == nil {
		t.Value = h0
		return nil
	}
	var h1 bool
	if err := json.Unmarshal(x, &h1); err == nil {
		t.Value = h1
		return nil
	}
	return &UnmarshalError{"unmarshal failed to match one of [DocumentRangeFormattingOptions bool]"}
}

func (t Or_ServerCapabilities_documentSymbolProvider) MarshalJSON() ([]byte, error) {
	switch x := t.Value.(type) {
	case DocumentSymbolOptions:
		return json.Marshal(x)
	case bool:
		return json.Marshal(x)
	case nil:
		return []byte("null"), nil
	}
	return nil, fmt.Errorf("type %T not one of [DocumentSymbolOptions bool]", t)
}

func (t *Or_ServerCapabilities_documentSymbolProvider) UnmarshalJSON(x []byte) error {
	if string(x) == "null" {
		t.Value = nil
		return nil
	}
	var h0 DocumentSymbolOptions
	if err := json.Unmarshal(x, &h0); err == nil {
		t.Value = h0
		return nil
	}
	var h1 bool
	if err := json.Unmarshal(x, &h1); err == nil {
		t.Value = h1
		return nil
	}
	return &UnmarshalError{"unmarshal failed to match one of [DocumentSymbolOptions bool]"}
}

func (t Or_ServerCapabilities_foldingRangeProvider) MarshalJSON() ([]byte, error) {
	switch x := t.Value.(type) {
	case FoldingRangeOptions:
		return json.Marshal(x)
	case FoldingRangeRegistrationOptions:
		return json.Marshal(x)
	case bool:
		return json.Marshal(x)
	case nil:
		return []byte("null"), nil
	}
	return nil, fmt.Errorf("type %T not one of [FoldingRangeOptions FoldingRangeRegistrationOptions bool]", t)
}

func (t *Or_ServerCapabilities_foldingRangeProvider) UnmarshalJSON(x []byte) error {
	if string(x) == "null" {
		t.Value = nil
		return nil
	}
	var h0 FoldingRangeOptions
	if err := json.Unmarshal(x, &h0); err == nil {
		t.Value = h0
		return nil
	}
	var h1 FoldingRangeRegistrationOptions
	if err := json.Unmarshal(x, &h1); err == nil {
		t.Value = h1
		return nil
	}
	var h2 bool
	if err := json.Unmarshal(x, &h2); err == nil {
		t.Value = h2
		return nil
	}
	return &UnmarshalError{"unmarshal failed to match one of [FoldingRangeOptions FoldingRangeRegistrationOptions bool]"}
}

func (t Or_ServerCapabilities_hoverProvider) MarshalJSON() ([]byte, error) {
	switch x := t.Value.(type) {
	case HoverOptions:
		return json.Marshal(x)
	case bool:
		return json.Marshal(x)
	case nil:
		return []byte("null"), nil
	}
	return nil, fmt.Errorf("type %T not one of [HoverOptions bool]", t)
}

func (t *Or_ServerCapabilities_hoverProvider) UnmarshalJSON(x []byte) error {
	if string(x) == "null" {
		t.Value = nil
		return nil
	}
	var h0 HoverOptions
	if err := json.Unmarshal(x, &h0); err == nil {
		t.Value = h0
		return nil
	}
	var h1 bool
	if err := json.Unmarshal(x, &h1); err == nil {
		t.Value = h1
		return nil
	}
	return &UnmarshalError{"unmarshal failed to match one of [HoverOptions bool]"}
}

func (t Or_ServerCapabilities_implementationProvider) MarshalJSON() ([]byte, error) {
	switch x := t.Value.(type) {
	case ImplementationOptions:
		return json.Marshal(x)
	case ImplementationRegistrationOptions:
		return json.Marshal(x)
	case bool:
		return json.Marshal(x)
	case nil:
		return []byte("null"), nil
	}
	return nil, fmt.Errorf("type %T not one of [ImplementationOptions ImplementationRegistrationOptions bool]", t)
}

func (t *Or_ServerCapabilities_implementationProvider) UnmarshalJSON(x []byte) error {
	if string(x) == "null" {
		t.Value = nil
		return nil
	}
	var h0 ImplementationOptions
	if err := json.Unmarshal(x, &h0); err == nil {
		t.Value = h0
		return nil
	}
	var h1 ImplementationRegistrationOptions
	if err := json.Unmarshal(x, &h1); err == nil {
		t.Value = h1
		return nil
	}
	var h2 bool
	if err := json.Unmarshal(x, &h2); err == nil {
		t.Value = h2
		return nil
	}
	return &UnmarshalError{"unmarshal failed to match one of [ImplementationOptions ImplementationRegistrationOptions bool]"}
}

func (t Or_ServerCapabilities_inlayHintProvider) MarshalJSON() ([]byte, error) {
	switch x := t.Value.(type) {
	case InlayHintOptions:
		return json.Marshal(x)
	case InlayHintRegistrationOptions:
		return json.Marshal(x)
	case bool:
		return json.Marshal(x)
	case nil:
		return []byte("null"), nil
	}
	return nil, fmt.Errorf("type %T not one of [InlayHintOptions InlayHintRegistrationOptions bool]", t)
}

func (t *Or_ServerCapabilities_inlayHintProvider) UnmarshalJSON(x []byte) error {
	if string(x) == "null" {
		t.Value = nil
		return nil
	}
	var h0 InlayHintOptions
	if err := json.Unmarshal(x, &h0); err == nil {
		t.Value = h0
		return nil
	}
	var h1 InlayHintRegistrationOptions
	if err := json.Unmarshal(x, &h1); err == nil {
		t.Value = h1
		return nil
	}
	var h2 bool
	if err := json.Unmarshal(x, &h2); err == nil {
		t.Value = h2
		return nil
	}
	return &UnmarshalError{"unmarshal failed to match one of [InlayHintOptions InlayHintRegistrationOptions bool]"}
}

func (t Or_ServerCapabilities_inlineCompletionProvider) MarshalJSON() ([]byte, error) {
	switch x := t.Value.(type) {
	case InlineCompletionOptions:
		return json.Marshal(x)
	case bool:
		return json.Marshal(x)
	case nil:
		return []byte("null"), nil
	}
	return nil, fmt.Errorf("type %T not one of [InlineCompletionOptions bool]", t)
}

func (t *Or_ServerCapabilities_inlineCompletionProvider) UnmarshalJSON(x []byte) error {
	if string(x) == "null" {
		t.Value = nil
		return nil
	}
	var h0 InlineCompletionOptions
	if err := json.Unmarshal(x, &h0); err == nil {
		t.Value = h0
		return nil
	}
	var h1 bool
	if err := json.Unmarshal(x, &h1); err == nil {
		t.Value = h1
		return nil
	}
	return &UnmarshalError{"unmarshal failed to match one of [InlineCompletionOptions bool]"}
}

func (t Or_ServerCapabilities_inlineValueProvider) MarshalJSON() ([]byte, error) {
	switch x := t.Value.(type) {
	case InlineValueOptions:
		return json.Marshal(x)
	case InlineValueRegistrationOptions:
		return json.Marshal(x)
	case bool:
		return json.Marshal(x)
	case nil:
		return []byte("null"), nil
	}
	return nil, fmt.Errorf("type %T not one of [InlineValueOptions InlineValueRegistrationOptions bool]", t)
}

func (t *Or_ServerCapabilities_inlineValueProvider) UnmarshalJSON(x []byte) error {
	if string(x) == "null" {
		t.Value = nil
		return nil
	}
	var h0 InlineValueOptions
	if err := json.Unmarshal(x, &h0); err == nil {
		t.Value = h0
		return nil
	}
	var h1 InlineValueRegistrationOptions
	if err := json.Unmarshal(x, &h1); err == nil {
		t.Value = h1
		return nil
	}
	var h2 bool
	if err := json.Unmarshal(x, &h2); err == nil {
		t.Value = h2
		return nil
	}
	return &UnmarshalError{"unmarshal failed to match one of [InlineValueOptions InlineValueRegistrationOptions bool]"}
}

func (t Or_ServerCapabilities_linkedEditingRangeProvider) MarshalJSON() ([]byte, error) {
	switch x := t.Value.(type) {
	case LinkedEditingRangeOptions:
		return json.Marshal(x)
	case LinkedEditingRangeRegistrationOptions:
		return json.Marshal(x)
	case bool:
		return json.Marshal(x)
	case nil:
		return []byte("null"), nil
	}
	return nil, fmt.Errorf("type %T not one of [LinkedEditingRangeOptions LinkedEditingRangeRegistrationOptions bool]", t)
}

func (t *Or_ServerCapabilities_linkedEditingRangeProvider) UnmarshalJSON(x []byte) error {
	if string(x) == "null" {
		t.Value = nil
		return nil
	}
	var h0 LinkedEditingRangeOptions
	if err := json.Unmarshal(x, &h0); err == nil {
		t.Value = h0
		return nil
	}
	var h1 LinkedEditingRangeRegistrationOptions
	if err := json.Unmarshal(x, &h1); err == nil {
		t.Value = h1
		return nil
	}
	var h2 bool
	if err := json.Unmarshal(x, &h2); err == nil {
		t.Value = h2
		return nil
	}
	return &UnmarshalError{"unmarshal failed to match one of [LinkedEditingRangeOptions LinkedEditingRangeRegistrationOptions bool]"}
}

func (t Or_ServerCapabilities_monikerProvider) MarshalJSON() ([]byte, error) {
	switch x := t.Value.(type) {
	case MonikerOptions:
		return json.Marshal(x)
	case MonikerRegistrationOptions:
		return json.Marshal(x)
	case bool:
		return json.Marshal(x)
	case nil:
		return []byte("null"), nil
	}
	return nil, fmt.Errorf("type %T not one of [MonikerOptions MonikerRegistrationOptions bool]", t)
}

func (t *Or_ServerCapabilities_monikerProvider) UnmarshalJSON(x []byte) error {
	if string(x) == "null" {
		t.Value = nil
		return nil
	}
	var h0 MonikerOptions
	if err := json.Unmarshal(x, &h0); err == nil {
		t.Value = h0
		return nil
	}
	var h1 MonikerRegistrationOptions
	if err := json.Unmarshal(x, &h1); err == nil {
		t.Value = h1
		return nil
	}
	var h2 bool
	if err := json.Unmarshal(x, &h2); err == nil {
		t.Value = h2
		return nil
	}
	return &UnmarshalError{"unmarshal failed to match one of [MonikerOptions MonikerRegistrationOptions bool]"}
}

func (t Or_ServerCapabilities_notebookDocumentSync) MarshalJSON() ([]byte, error) {
	switch x := t.Value.(type) {
	case NotebookDocumentSyncOptions:
		return json.Marshal(x)
	case NotebookDocumentSyncRegistrationOptions:
		return json.Marshal(x)
	case nil:
		return []byte("null"), nil
	}
	return nil, fmt.Errorf("type %T not one of [NotebookDocumentSyncOptions NotebookDocumentSyncRegistrationOptions]", t)
}

func (t *Or_ServerCapabilities_notebookDocumentSync) UnmarshalJSON(x []byte) error {
	if string(x) == "null" {
		t.Value = nil
		return nil
	}
	var h0 NotebookDocumentSyncOptions
	if err := json.Unmarshal(x, &h0); err == nil {
		t.Value = h0
		return nil
	}
	var h1 NotebookDocumentSyncRegistrationOptions
	if err := json.Unmarshal(x, &h1); err == nil {
		t.Value = h1
		return nil
	}
	return &UnmarshalError{"unmarshal failed to match one of [NotebookDocumentSyncOptions NotebookDocumentSyncRegistrationOptions]"}
}

func (t Or_ServerCapabilities_referencesProvider) MarshalJSON() ([]byte, error) {
	switch x := t.Value.(type) {
	case ReferenceOptions:
		return json.Marshal(x)
	case bool:
		return json.Marshal(x)
	case nil:
		return []byte("null"), nil
	}
	return nil, fmt.Errorf("type %T not one of [ReferenceOptions bool]", t)
}

func (t *Or_ServerCapabilities_referencesProvider) UnmarshalJSON(x []byte) error {
	if string(x) == "null" {
		t.Value = nil
		return nil
	}
	var h0 ReferenceOptions
	if err := json.Unmarshal(x, &h0); err == nil {
		t.Value = h0
		return nil
	}
	var h1 bool
	if err := json.Unmarshal(x, &h1); err == nil {
		t.Value = h1
		return nil
	}
	return &UnmarshalError{"unmarshal failed to match one of [ReferenceOptions bool]"}
}

func (t Or_ServerCapabilities_renameProvider) MarshalJSON() ([]byte, error) {
	switch x := t.Value.(type) {
	case RenameOptions:
		return json.Marshal(x)
	case bool:
		return json.Marshal(x)
	case nil:
		return []byte("null"), nil
	}
	return nil, fmt.Errorf("type %T not one of [RenameOptions bool]", t)
}

func (t *Or_ServerCapabilities_renameProvider) UnmarshalJSON(x []byte) error {
	if string(x) == "null" {
		t.Value = nil
		return nil
	}
	var h0 RenameOptions
	if err := json.Unmarshal(x, &h0); err == nil {
		t.Value = h0
		return nil
	}
	var h1 bool
	if err := json.Unmarshal(x, &h1); err == nil {
		t.Value = h1
		return nil
	}
	return &UnmarshalError{"unmarshal failed to match one of [RenameOptions bool]"}
}

func (t Or_ServerCapabilities_selectionRangeProvider) MarshalJSON() ([]byte, error) {
	switch x := t.Value.(type) {
	case SelectionRangeOptions:
		return json.Marshal(x)
	case SelectionRangeRegistrationOptions:
		return json.Marshal(x)
	case bool:
		return json.Marshal(x)
	case nil:
		return []byte("null"), nil
	}
	return nil, fmt.Errorf("type %T not one of [SelectionRangeOptions SelectionRangeRegistrationOptions bool]", t)
}

func (t *Or_ServerCapabilities_selectionRangeProvider) UnmarshalJSON(x []byte) error {
	if string(x) == "null" {
		t.Value = nil
		return nil
	}
	var h0 SelectionRangeOptions
	if err := json.Unmarshal(x, &h0); err == nil {
		t.Value = h0
		return nil
	}
	var h1 SelectionRangeRegistrationOptions
	if err := json.Unmarshal(x, &h1); err == nil {
		t.Value = h1
		return nil
	}
	var h2 bool
	if err := json.Unmarshal(x, &h2); err == nil {
		t.Value = h2
		return nil
	}
	return &UnmarshalError{"unmarshal failed to match one of [SelectionRangeOptions SelectionRangeRegistrationOptions bool]"}
}

func (t Or_ServerCapabilities_semanticTokensProvider) MarshalJSON() ([]byte, error) {
	switch x := t.Value.(type) {
	case SemanticTokensOptions:
		return json.Marshal(x)
	case SemanticTokensRegistrationOptions:
		return json.Marshal(x)
	case nil:
		return []byte("null"), nil
	}
	return nil, fmt.Errorf("type %T not one of [SemanticTokensOptions SemanticTokensRegistrationOptions]", t)
}

func (t *Or_ServerCapabilities_semanticTokensProvider) UnmarshalJSON(x []byte) error {
	if string(x) == "null" {
		t.Value = nil
		return nil
	}
	var h0 SemanticTokensOptions
	if err := json.Unmarshal(x, &h0); err == nil {
		t.Value = h0
		return nil
	}
	var h1 SemanticTokensRegistrationOptions
	if err := json.Unmarshal(x, &h1); err == nil {
		t.Value = h1
		return nil
	}
	return &UnmarshalError{"unmarshal failed to match one of [SemanticTokensOptions SemanticTokensRegistrationOptions]"}
}

func (t Or_ServerCapabilities_textDocumentSync) MarshalJSON() ([]byte, error) {
	switch x := t.Value.(type) {
	case TextDocumentSyncKind:
		return json.Marshal(x)
	case TextDocumentSyncOptions:
		return json.Marshal(x)
	case nil:
		return []byte("null"), nil
	}
	return nil, fmt.Errorf("type %T not one of [TextDocumentSyncKind TextDocumentSyncOptions]", t)
}

func (t *Or_ServerCapabilities_textDocumentSync) UnmarshalJSON(x []byte) error {
	if string(x) == "null" {
		t.Value = nil
		return nil
	}
	var h0 TextDocumentSyncKind
	if err := json.Unmarshal(x, &h0); err == nil {
		t.Value = h0
		return nil
	}
	var h1 TextDocumentSyncOptions
	if err := json.Unmarshal(x, &h1); err == nil {
		t.Value = h1
		return nil
	}
	return &UnmarshalError{"unmarshal failed to match one of [TextDocumentSyncKind TextDocumentSyncOptions]"}
}

func (t Or_ServerCapabilities_typeDefinitionProvider) MarshalJSON() ([]byte, error) {
	switch x := t.Value.(type) {
	case TypeDefinitionOptions:
		return json.Marshal(x)
	case TypeDefinitionRegistrationOptions:
		return json.Marshal(x)
	case bool:
		return json.Marshal(x)
	case nil:
		return []byte("null"), nil
	}
	return nil, fmt.Errorf("type %T not one of [TypeDefinitionOptions TypeDefinitionRegistrationOptions bool]", t)
}

func (t *Or_ServerCapabilities_typeDefinitionProvider) UnmarshalJSON(x []byte) error {
	if string(x) == "null" {
		t.Value = nil
		return nil
	}
	var h0 TypeDefinitionOptions
	if err := json.Unmarshal(x, &h0); err == nil {
		t.Value = h0
		return nil
	}
	var h1 TypeDefinitionRegistrationOptions
	if err := json.Unmarshal(x, &h1); err == nil {
		t.Value = h1
		return nil
	}
	var h2 bool
	if err := json.Unmarshal(x, &h2); err == nil {
		t.Value = h2
		return nil
	}
	return &UnmarshalError{"unmarshal failed to match one of [TypeDefinitionOptions TypeDefinitionRegistrationOptions bool]"}
}

func (t Or_ServerCapabilities_typeHierarchyProvider) MarshalJSON() ([]byte, error) {
	switch x := t.Value.(type) {
	case TypeHierarchyOptions:
		return json.Marshal(x)
	case TypeHierarchyRegistrationOptions:
		return json.Marshal(x)
	case bool:
		return json.Marshal(x)
	case nil:
		return []byte("null"), nil
	}
	return nil, fmt.Errorf("type %T not one of [TypeHierarchyOptions TypeHierarchyRegistrationOptions bool]", t)
}

func (t *Or_ServerCapabilities_typeHierarchyProvider) UnmarshalJSON(x []byte) error {
	if string(x) == "null" {
		t.Value = nil
		return nil
	}
	var h0 TypeHierarchyOptions
	if err := json.Unmarshal(x, &h0); err == nil {
		t.Value = h0
		return nil
	}
	var h1 TypeHierarchyRegistrationOptions
	if err := json.Unmarshal(x, &h1); err == nil {
		t.Value = h1
		return nil
	}
	var h2 bool
	if err := json.Unmarshal(x, &h2); err == nil {
		t.Value = h2
		return nil
	}
	return &UnmarshalError{"unmarshal failed to match one of [TypeHierarchyOptions TypeHierarchyRegistrationOptions bool]"}
}

func (t Or_ServerCapabilities_workspaceSymbolProvider) MarshalJSON() ([]byte, error) {
	switch x := t.Value.(type) {
	case WorkspaceSymbolOptions:
		return json.Marshal(x)
	case bool:
		return json.Marshal(x)
	case nil:
		return []byte("null"), nil
	}
	return nil, fmt.Errorf("type %T not one of [WorkspaceSymbolOptions bool]", t)
}

func (t *Or_ServerCapabilities_workspaceSymbolProvider) UnmarshalJSON(x []byte) error {
	if string(x) == "null" {
		t.Value = nil
		return nil
	}
	var h0 WorkspaceSymbolOptions
	if err := json.Unmarshal(x, &h0); err == nil {
		t.Value = h0
		return nil
	}
	var h1 bool
	if err := json.Unmarshal(x, &h1); err == nil {
		t.Value = h1
		return nil
	}
	return &UnmarshalError{"unmarshal failed to match one of [WorkspaceSymbolOptions bool]"}
}

func (t Or_SignatureInformation_documentation) MarshalJSON() ([]byte, error) {
	switch x := t.Value.(type) {
	case MarkupContent:
		return json.Marshal(x)
	case string:
		return json.Marshal(x)
	case nil:
		return []byte("null"), nil
	}
	return nil, fmt.Errorf("type %T not one of [MarkupContent string]", t)
}

func (t *Or_SignatureInformation_documentation) UnmarshalJSON(x []byte) error {
	if string(x) == "null" {
		t.Value = nil
		return nil
	}
	var h0 MarkupContent
	if err := json.Unmarshal(x, &h0); err == nil {
		t.Value = h0
		return nil
	}
	var h1 string
	if err := json.Unmarshal(x, &h1); err == nil {
		t.Value = h1
		return nil
	}
	return &UnmarshalError{"unmarshal failed to match one of [MarkupContent string]"}
}

func (t Or_TextDocumentEdit_edits_Elem) MarshalJSON() ([]byte, error) {
	switch x := t.Value.(type) {
	case AnnotatedTextEdit:
		return json.Marshal(x)
	case SnippetTextEdit:
		return json.Marshal(x)
	case TextEdit:
		return json.Marshal(x)
	case nil:
		return []byte("null"), nil
	}
	return nil, fmt.Errorf("type %T not one of [AnnotatedTextEdit SnippetTextEdit TextEdit]", t)
}

func (t *Or_TextDocumentEdit_edits_Elem) UnmarshalJSON(x []byte) error {
	if string(x) == "null" {
		t.Value = nil
		return nil
	}
	var h0 AnnotatedTextEdit
	if err := json.Unmarshal(x, &h0); err == nil {
		t.Value = h0
		return nil
	}
	var h1 SnippetTextEdit
	if err := json.Unmarshal(x, &h1); err == nil {
		t.Value = h1
		return nil
	}
	var h2 TextEdit
	if err := json.Unmarshal(x, &h2); err == nil {
		t.Value = h2
		return nil
	}
	return &UnmarshalError{"unmarshal failed to match one of [AnnotatedTextEdit SnippetTextEdit TextEdit]"}
}

func (t Or_TextDocumentFilter) MarshalJSON() ([]byte, error) {
	switch x := t.Value.(type) {
	case TextDocumentFilterLanguage:
		return json.Marshal(x)
	case TextDocumentFilterPattern:
		return json.Marshal(x)
	case TextDocumentFilterScheme:
		return json.Marshal(x)
	case nil:
		return []byte("null"), nil
	}
	return nil, fmt.Errorf("type %T not one of [TextDocumentFilterLanguage TextDocumentFilterPattern TextDocumentFilterScheme]", t)
}

func (t *Or_TextDocumentFilter) UnmarshalJSON(x []byte) error {
	if string(x) == "null" {
		t.Value = nil
		return nil
	}
	var h0 TextDocumentFilterLanguage
	if err := json.Unmarshal(x, &h0); err == nil {
		t.Value = h0
		return nil
	}
	var h1 TextDocumentFilterPattern
	if err := json.Unmarshal(x, &h1); err == nil {
		t.Value = h1
		return nil
	}
	var h2 TextDocumentFilterScheme
	if err := json.Unmarshal(x, &h2); err == nil {
		t.Value = h2
		return nil
	}
	return &UnmarshalError{"unmarshal failed to match one of [TextDocumentFilterLanguage TextDocumentFilterPattern TextDocumentFilterScheme]"}
}

func (t Or_TextDocumentSyncOptions_save) MarshalJSON() ([]byte, error) {
	switch x := t.Value.(type) {
	case SaveOptions:
		return json.Marshal(x)
	case bool:
		return json.Marshal(x)
	case nil:
		return []byte("null"), nil
	}
	return nil, fmt.Errorf("type %T not one of [SaveOptions bool]", t)
}

func (t *Or_TextDocumentSyncOptions_save) UnmarshalJSON(x []byte) error {
	if string(x) == "null" {
		t.Value = nil
		return nil
	}
	var h0 SaveOptions
	if err := json.Unmarshal(x, &h0); err == nil {
		t.Value = h0
		return nil
	}
	var h1 bool
	if err := json.Unmarshal(x, &h1); err == nil {
		t.Value = h1
		return nil
	}
	return &UnmarshalError{"unmarshal failed to match one of [SaveOptions bool]"}
}

func (t Or_WorkspaceDocumentDiagnosticReport) MarshalJSON() ([]byte, error) {
	switch x := t.Value.(type) {
	case WorkspaceFullDocumentDiagnosticReport:
		return json.Marshal(x)
	case WorkspaceUnchangedDocumentDiagnosticReport:
		return json.Marshal(x)
	case nil:
		return []byte("null"), nil
	}
	return nil, fmt.Errorf("type %T not one of [WorkspaceFullDocumentDiagnosticReport WorkspaceUnchangedDocumentDiagnosticReport]", t)
}

func (t *Or_WorkspaceDocumentDiagnosticReport) UnmarshalJSON(x []byte) error {
	if string(x) == "null" {
		t.Value = nil
		return nil
	}
	var h0 WorkspaceFullDocumentDiagnosticReport
	if err := json.Unmarshal(x, &h0); err == nil {
		t.Value = h0
		return nil
	}
	var h1 WorkspaceUnchangedDocumentDiagnosticReport
	if err := json.Unmarshal(x, &h1); err == nil {
		t.Value = h1
		return nil
	}
	return &UnmarshalError{"unmarshal failed to match one of [WorkspaceFullDocumentDiagnosticReport WorkspaceUnchangedDocumentDiagnosticReport]"}
}

func (t Or_WorkspaceEdit_documentChanges_Elem) MarshalJSON() ([]byte, error) {
	switch x := t.Value.(type) {
	case CreateFile:
		return json.Marshal(x)
	case DeleteFile:
		return json.Marshal(x)
	case RenameFile:
		return json.Marshal(x)
	case TextDocumentEdit:
		return json.Marshal(x)
	case nil:
		return []byte("null"), nil
	}
	return nil, fmt.Errorf("type %T not one of [CreateFile DeleteFile RenameFile TextDocumentEdit]", t)
}

func (t *Or_WorkspaceEdit_documentChanges_Elem) UnmarshalJSON(x []byte) error {
	if string(x) == "null" {
		t.Value = nil
		return nil
	}
	var h0 CreateFile
	if err := json.Unmarshal(x, &h0); err == nil {
		t.Value = h0
		return nil
	}
	var h1 DeleteFile
	if err := json.Unmarshal(x, &h1); err == nil {
		t.Value = h1
		return nil
	}
	var h2 RenameFile
	if err := json.Unmarshal(x, &h2); err == nil {
		t.Value = h2
		return nil
	}
	var h3 TextDocumentEdit
	if err := json.Unmarshal(x, &h3); err == nil {
		t.Value = h3
		return nil
	}
	return &UnmarshalError{"unmarshal failed to match one of [CreateFile DeleteFile RenameFile TextDocumentEdit]"}
}

func (t Or_WorkspaceOptions_textDocumentContent) MarshalJSON() ([]byte, error) {
	switch x := t.Value.(type) {
	case TextDocumentContentOptions:
		return json.Marshal(x)
	case TextDocumentContentRegistrationOptions:
		return json.Marshal(x)
	case nil:
		return []byte("null"), nil
	}
	return nil, fmt.Errorf("type %T not one of [TextDocumentContentOptions TextDocumentContentRegistrationOptions]", t)
}

func (t *Or_WorkspaceOptions_textDocumentContent) UnmarshalJSON(x []byte) error {
	if string(x) == "null" {
		t.Value = nil
		return nil
	}
	var h0 TextDocumentContentOptions
	if err := json.Unmarshal(x, &h0); err == nil {
		t.Value = h0
		return nil
	}
	var h1 TextDocumentContentRegistrationOptions
	if err := json.Unmarshal(x, &h1); err == nil {
		t.Value = h1
		return nil
	}
	return &UnmarshalError{"unmarshal failed to match one of [TextDocumentContentOptions TextDocumentContentRegistrationOptions]"}
}

func (t Or_textDocument_declaration) MarshalJSON() ([]byte, error) {
	switch x := t.Value.(type) {
	case Declaration:
		return json.Marshal(x)
	case []DeclarationLink:
		return json.Marshal(x)
	case nil:
		return []byte("null"), nil
	}
	return nil, fmt.Errorf("type %T not one of [Declaration []DeclarationLink]", t)
}

func (t *Or_textDocument_declaration) UnmarshalJSON(x []byte) error {
	if string(x) == "null" {
		t.Value = nil
		return nil
	}
	var h0 Declaration
	if err := json.Unmarshal(x, &h0); err == nil {
		t.Value = h0
		return nil
	}
	var h1 []DeclarationLink
	if err := json.Unmarshal(x, &h1); err == nil {
		t.Value = h1
		return nil
	}
	return &UnmarshalError{"unmarshal failed to match one of [Declaration []DeclarationLink]"}
}
