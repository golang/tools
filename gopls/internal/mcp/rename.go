// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package mcp

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/tools/gopls/internal/cache"
	"golang.org/x/tools/gopls/internal/file"
	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/internal/mcp"
)

type renameParams struct {
	File    string `json:"file"`
	Line    uint32 `json:"line"`
	Column  uint32 `json:"column"`
	NewName string `json:"newName"`
	DryRun  *bool  `json:"dryRun,omitempty"`
}

func (h *handler) renameTool() *mcp.ServerTool {
	const desc = `Rename a Go symbol at the specified location.

This tool renames a Go symbol (variable, function, type, etc.) at the given
file position. By default it shows what changes would be made (dry run).
Set dryRun to false to actually apply the changes.

The line and column numbers are zero-based, following LSP conventions.
`
	return mcp.NewServerTool(
		"go_rename",
		desc,
		h.renameHandler,
		mcp.Input(
			mcp.Property("file", mcp.Description("the absolute path to the file containing the symbol")),
			mcp.Property("line", mcp.Description("line number (zero-based)")),
			mcp.Property("column", mcp.Description("column number (zero-based)")),
			mcp.Property("newName", mcp.Description("the new name for the symbol")),
			mcp.Property("dryRun", mcp.Description("if true (default), show changes without applying them; if false, apply the changes"), mcp.Required(false)),
		),
	)
}

func (h *handler) renameHandler(
	ctx context.Context, _ *mcp.ServerSession, params *mcp.CallToolParamsFor[renameParams],
) (*mcp.CallToolResultFor[any], error) {
	fh, snapshot, release, err := h.fileOf(ctx, params.Arguments.File)
	if err != nil {
		return nil, err
	}
	defer release()

	if snapshot.FileKind(fh) != file.Go {
		return nil, fmt.Errorf("can't rename symbols in non-Go files")
	}

	if params.Arguments.NewName == "" {
		return nil, fmt.Errorf("newName cannot be empty")
	}

	uri := protocol.URIFromPath(params.Arguments.File)
	position := protocol.Position{
		Line:      params.Arguments.Line,
		Character: params.Arguments.Column,
	}

	renameParams := &protocol.RenameParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: uri},
		Position:     position,
		NewName:      params.Arguments.NewName,
	}

	workspaceEdit, err := h.lspServer.Rename(ctx, renameParams)
	if err != nil {
		return nil, fmt.Errorf("failed to perform rename: %v", err)
	}

	if workspaceEdit == nil {
		return textResult("No changes would be made - symbol may not be renameable at this location."), nil
	}

	// WorkspaceEdit can return changes in two formats:
	// - Changes: simple map of file URI to text edits (legacy format)
	// - DocumentChanges: array of document changes with versioning support (preferred format)
	if len(workspaceEdit.Changes) == 0 && len(workspaceEdit.DocumentChanges) == 0 {
		return textResult("No changes would be made."), nil
	}

	// Check if this is a dry run (default to true if not specified)
	dryRun := true
	if params.Arguments.DryRun != nil {
		dryRun = *params.Arguments.DryRun
	}

	if dryRun {
		return formatWorkspaceEdit(ctx, snapshot, workspaceEdit)
	}
	return applyWorkspaceEdit(ctx, snapshot, workspaceEdit)
}

func formatWorkspaceEdit(
	ctx context.Context, snapshot *cache.Snapshot, edit *protocol.WorkspaceEdit,
) (*mcp.CallToolResultFor[any], error) {
	var builder strings.Builder

	totalChanges := 0
	fileCount := 0

	// Count changes from legacy Changes format
	for _, edits := range edit.Changes {
		totalChanges += len(edits)
		fileCount++
	}

	// Count changes from modern DocumentChanges format
	for _, docChange := range edit.DocumentChanges {
		if docChange.TextDocumentEdit != nil {
			totalChanges += len(docChange.TextDocumentEdit.Edits)
			fileCount++
		}
	}

	if totalChanges == 0 {
		return textResult("No changes would be made."), nil
	}

	fmt.Fprintf(&builder, "Rename would make %d changes across %d files:\n\n", totalChanges, fileCount)

	// Process legacy Changes format
	for uri, edits := range edit.Changes {
		if err := formatFileChanges(&builder, ctx, snapshot, uri, edits); err != nil {
			return nil, err
		}
	}

	// Process modern DocumentChanges format
	for _, docChange := range edit.DocumentChanges {
		if docChange.TextDocumentEdit != nil {
			uri := docChange.TextDocumentEdit.TextDocument.URI
			edits := protocol.AsTextEdits(docChange.TextDocumentEdit.Edits)
			if err := formatFileChanges(&builder, ctx, snapshot, uri, edits); err != nil {
				return nil, err
			}
		}
	}

	return textResult(builder.String()), nil
}

func applyWorkspaceEdit(
	ctx context.Context, snapshot *cache.Snapshot, edit *protocol.WorkspaceEdit,
) (*mcp.CallToolResultFor[any], error) {
	var builder strings.Builder
	totalChanges := 0
	fileCount := 0

	// Apply changes from legacy Changes format
	for uri, edits := range edit.Changes {
		if err := applyFileChanges(&builder, ctx, snapshot, uri, edits); err != nil {
			return nil, err
		}
		totalChanges += len(edits)
		fileCount++
	}

	// Apply changes from modern DocumentChanges format
	for _, docChange := range edit.DocumentChanges {
		if docChange.TextDocumentEdit != nil {
			uri := docChange.TextDocumentEdit.TextDocument.URI
			edits := protocol.AsTextEdits(docChange.TextDocumentEdit.Edits)
			if err := applyFileChanges(&builder, ctx, snapshot, uri, edits); err != nil {
				return nil, err
			}
			totalChanges += len(edits)
			fileCount++
		}
	}

	if totalChanges == 0 {
		return textResult("No changes were applied."), nil
	}

	fmt.Fprintf(&builder, "Successfully applied %d changes across %d files:\n\n", totalChanges, fileCount)
	return textResult(builder.String()), nil
}

func applyFileChanges(
	builder *strings.Builder,
	ctx context.Context,
	snapshot *cache.Snapshot,
	uri protocol.DocumentURI,
	edits []protocol.TextEdit,
) error {
	fh, err := snapshot.ReadFile(ctx, uri)
	if err != nil {
		return fmt.Errorf("failed to read file %s: %v", uri.Path(), err)
	}

	content, err := fh.Content()
	if err != nil {
		return fmt.Errorf("failed to read file content %s: %v", uri.Path(), err)
	}

	// Create a mapper for the file content
	mapper := protocol.NewMapper(uri, content)

	// Apply the edits to get the new content
	newContent, _, err := protocol.ApplyEdits(mapper, edits)
	if err != nil {
		return fmt.Errorf("failed to apply edits to %s: %v", uri.Path(), err)
	}

	// Write the new content back to the file
	if err := os.WriteFile(uri.Path(), newContent, 0644); err != nil {
		return fmt.Errorf("failed to write file %s: %v", uri.Path(), err)
	}

	fmt.Fprintf(builder, "✓ %s (%d changes)\n", filepath.ToSlash(uri.Path()), len(edits))
	return nil
}

func formatFileChanges(
	builder *strings.Builder,
	ctx context.Context,
	snapshot *cache.Snapshot,
	uri protocol.DocumentURI,
	edits []protocol.TextEdit,
) error {
	fmt.Fprintf(builder, "File: %s\n", filepath.ToSlash(uri.Path()))

	fh, err := snapshot.ReadFile(ctx, uri)
	if err != nil {
		fmt.Fprintf(builder, "  Error reading file: %v\n\n", err)
		return nil
	}

	content, err := fh.Content()
	if err != nil {
		fmt.Fprintf(builder, "  Error reading file content: %v\n\n", err)
		return nil
	}

	lines := strings.Split(string(content), "\n")

	for i, edit := range edits {
		fmt.Fprintf(builder, "  Change %d (line %d, col %d-%d): ", i+1, edit.Range.Start.Line+1, edit.Range.Start.Character+1, edit.Range.End.Character+1)

		if int(edit.Range.Start.Line) < len(lines) {
			oldText := ""
			if edit.Range.Start.Line == edit.Range.End.Line {
				line := lines[edit.Range.Start.Line]
				if int(edit.Range.Start.Character) < len(line) && int(edit.Range.End.Character) <= len(line) {
					oldText = line[edit.Range.Start.Character:edit.Range.End.Character]
				}
			}
			fmt.Fprintf(builder, "'%s' → '%s'\n", oldText, edit.NewText)
		} else {
			fmt.Fprintf(builder, "→ '%s'\n", edit.NewText)
		}
	}
	builder.WriteString("\n")
	return nil
}
