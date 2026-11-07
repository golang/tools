// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package mcp

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"golang.org/x/tools/gopls/internal/protocol"
)

type searchParams struct {
	Query string `json:"query" jsonschema:"the fuzzy search query to use for matching symbols"`
}

func (h *handler) searchHandler(ctx context.Context, req *mcp.CallToolRequest, params searchParams) (*mcp.CallToolResult, any, error) {
	countGoSearchMCP.Inc()
	query := params.Query
	if len(query) == 0 {
		return nil, nil, fmt.Errorf("empty query")
	}
	syms, err := h.lspServer.Symbol(ctx, &protocol.WorkspaceSymbolParams{
		Query: params.Query,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to execute symbol query: %v", err)
	}
	if len(syms) == 0 {
		return textResult("No symbols found."), nil, nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Top symbol matches:\n")
	for _, sym := range syms {
		fmt.Fprintf(&b, "\t%s (%s in `%s`)\n", sym.Name, kindName(sym.Kind), sym.Location.URI.Path())
	}
	return textResult(b.String()), nil, nil
}

// kindName returns the adjusted name for the given symbol kind,
// fixing LSP conventions that don't work for go, like 'Class'.
func kindName(k protocol.SymbolKind) string {
	if k == protocol.Class {
		return "Type"
	}
	return fmt.Sprint(k)
}
