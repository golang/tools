// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package mcp

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"golang.org/x/tools/gopls/internal/cache"
	"golang.org/x/tools/gopls/internal/file"
	"golang.org/x/tools/gopls/internal/golang"
	"golang.org/x/tools/gopls/internal/protocol"
)

type renameSymbolParams struct {
	File    string `json:"file" jsonschema:"the absolute path to the file containing the symbol"`
	Symbol  string `json:"symbol" jsonschema:"the symbol or qualified symbol"`
	NewName string `json:"new_name" jsonschema:"the new name for the symbol"`
}

func (h *handler) renameSymbolHandler(ctx context.Context, req *mcp.CallToolRequest, params renameSymbolParams) (*mcp.CallToolResult, any, error) {
	countGoRenameSymbolMCP.Inc()
	fh, snapshot, release, err := h.fileOf(ctx, params.File)
	if err != nil {
		return nil, nil, err
	}
	defer release()

	if snapshot.FileKind(fh) != file.Go {
		return nil, nil, fmt.Errorf("can't rename symbols in non-Go files")
	}
	loc, err := symbolLocation(ctx, snapshot, fh.URI(), params.Symbol)
	if err != nil {
		return nil, nil, err
	}
	changes, err := golang.Rename(ctx, snapshot, fh, loc.Range, params.NewName)
	if err != nil {
		return nil, nil, err
	}
	var builder strings.Builder
	if err := formatRenameChanges(ctx, snapshot, &builder, changes); err != nil {
		return nil, nil, err
	}
	return textResult(builder.String()), nil, nil
}

// formatRenameChanges converts the list of DocumentChange to a unified diff and writes them to the specified buffer.
func formatRenameChanges(ctx context.Context, snapshot *cache.Snapshot, w *strings.Builder, changes []protocol.DocumentChange) error {
	w.WriteString("The following changes are necessary to rename the symbol:\n")
	if err := writeUnifiedDiff(ctx, snapshot, w, changes); err != nil {
		return err
	}
	w.WriteString("\n")
	return nil
}
