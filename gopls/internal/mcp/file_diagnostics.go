// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package mcp

// This file defines the "diagnostics" operation, which is responsible for
// returning diagnostics for the input file.

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"golang.org/x/tools/gopls/internal/cache"
	"golang.org/x/tools/gopls/internal/golang"
	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/internal/diff"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type diagnosticsParams struct {
	File string `json:"file" jsonschema:"the absolute path to the file to diagnose"`
}

func (h *handler) fileDiagnosticsHandler(ctx context.Context, req *mcp.CallToolRequest, params diagnosticsParams) (*mcp.CallToolResult, any, error) {
	countGoFileDiagnosticsMCP.Inc()
	fh, snapshot, release, err := h.fileOf(ctx, params.File)
	if err != nil {
		return nil, nil, err
	}
	defer release()

	diagnostics, fixes, err := h.diagnoseFile(ctx, snapshot, fh.URI())
	if err != nil {
		return nil, nil, err
	}

	var builder strings.Builder
	if len(diagnostics) == 0 {
		return textResult("No diagnostics"), nil, nil
	}

	if err := summarizeDiagnostics(ctx, snapshot, &builder, diagnostics, fixes); err != nil {
		return nil, nil, err
	}

	return textResult(builder.String()), nil, nil
}

// diagnoseFile diagnoses a single file, including go/analysis and quick fixes.
func (h *handler) diagnoseFile(ctx context.Context, snapshot *cache.Snapshot, uri protocol.DocumentURI) ([]*cache.Diagnostic, map[*cache.Diagnostic]*protocol.CodeAction, error) {
	diagnostics, err := golang.DiagnoseFile(ctx, snapshot, uri)
	if err != nil {
		return nil, nil, err
	}
	if len(diagnostics) == 0 {
		return nil, nil, nil
	}

	// LSP [protocol.Diagnostic]s do not carry code edits directly.
	// Instead, gopls provides associated [protocol.CodeAction]s with their
	// diagnostics field populated.
	// Ignore errors. It is still valuable to provide only the diagnostic
	// without any text edits.
	// TODO(hxjiang): support code actions that returns call back command.
	actions, _ := h.lspServer.CodeAction(ctx, &protocol.CodeActionParams{
		TextDocument: protocol.TextDocumentIdentifier{
			URI: uri,
		},
		Context: protocol.CodeActionContext{
			Only:        []protocol.CodeActionKind{protocol.QuickFix},
			Diagnostics: cache.ToProtocolDiagnostics(diagnostics...),
		},
	})

	type key struct {
		Message string
		Range   protocol.Range
	}

	actionMap := make(map[key]*protocol.CodeAction)
	for _, action := range actions {
		for _, d := range action.Diagnostics {
			k := key{d.Message, d.Range}
			if alt, ok := actionMap[k]; !ok || !alt.IsPreferred && action.IsPreferred {
				actionMap[k] = &action
			}
		}
	}

	fixes := make(map[*cache.Diagnostic]*protocol.CodeAction)
	for _, d := range diagnostics {
		if fix, ok := actionMap[key{d.Message, d.Range}]; ok {
			fixes[d] = fix
		}
	}
	return diagnostics, fixes, nil
}

func summarizeDiagnostics(ctx context.Context, snapshot *cache.Snapshot, w *strings.Builder, diagnostics []*cache.Diagnostic, fixes map[*cache.Diagnostic]*protocol.CodeAction) error {
	for _, d := range diagnostics {
		fmt.Fprintf(w, "%d:%d-%d:%d: [%s] %s\n", d.Range.Start.Line, d.Range.Start.Character, d.Range.End.Line, d.Range.End.Character, d.Severity, d.Message)

		fix, ok := fixes[d]
		if ok && fix.Edit != nil {
			w.WriteString("Fix:\n")
			if err := writeUnifiedDiff(ctx, snapshot, w, fix.Edit.DocumentChanges); err != nil {
				return err
			}
			w.WriteString("\n")
		}
	}
	return nil
}

// writeUnifiedDiff converts each [protocol.DocumentChange] into a separate
// unified diff and write to the input writer.
//
// All returned diffs use forward slash ('/') as the file path separator for
// consistency, regardless of the original system's separator.
// Multiple changes targeting the same file are not consolidated.
//
// TODO(hxjiang): consolidate diffs to the same file.
func writeUnifiedDiff(ctx context.Context, snapshot *cache.Snapshot, w *strings.Builder, changes []protocol.DocumentChange) error {
	for _, change := range changes {
		// The before-and-after states for the file change.
		var (
			oldFile, newFile       string
			oldContent, newContent string
		)
		switch {
		case change.CreateFile != nil:
			oldFile, newFile = "/dev/null", filepath.ToSlash(change.CreateFile.URI.Path())
			oldContent, newContent = "", ""
		case change.DeleteFile != nil:
			fh, err := snapshot.ReadFile(ctx, change.DeleteFile.URI)
			if err != nil {
				return err
			}
			content, err := fh.Content()
			if err != nil {
				return err
			}
			oldFile, newFile = filepath.ToSlash(change.DeleteFile.URI.Path()), "/dev/null"
			oldContent, newContent = string(content), ""
		case change.RenameFile != nil:
			fh, err := snapshot.ReadFile(ctx, change.RenameFile.OldURI)
			if err != nil {
				return err
			}
			content, err := fh.Content()
			if err != nil {
				return err
			}
			oldFile = filepath.ToSlash(change.RenameFile.OldURI.Path())
			newFile = filepath.ToSlash(change.RenameFile.NewURI.Path())
			oldContent, newContent = string(content), string(content)
		case change.TextDocumentEdit != nil:
			fh, err := snapshot.ReadFile(ctx, change.TextDocumentEdit.TextDocument.URI)
			if err != nil {
				return err
			}

			// Assumes gopls never return AnnotatedTextEdit.
			sorted := protocol.AsTextEdits(change.TextDocumentEdit.Edits)

			// As stated by the LSP, text edits ranges must never overlap.
			// https://microsoft.github.io/language-server-protocol/specifications/lsp/3.17/specification/#textEditArray
			slices.SortStableFunc(sorted, func(a, b protocol.TextEdit) int {
				return protocol.CompareRange(a.Range, b.Range)
			})

			content, err := fh.Content()
			if err != nil {
				return err
			}

			var newSrc bytes.Buffer
			{
				mapper := protocol.NewMapper(fh.URI(), content)

				start := 0
				for _, edit := range sorted {
					l, r, err := mapper.RangeOffsets(edit.Range)
					if err != nil {
						return err
					}

					newSrc.Write(content[start:l])
					newSrc.WriteString(edit.NewText)

					start = r
				}
				newSrc.Write(content[start:])
			}

			oldFile, newFile = filepath.ToSlash(fh.URI().Path()), filepath.ToSlash(fh.URI().Path())
			oldContent, newContent = string(content), newSrc.String()
		default:
			continue // this shouldn't happen
		}
		w.WriteString(diff.Unified(oldFile, newFile, oldContent, newContent))
		w.WriteString("\n")
	}
	return nil
}
