// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package mcp

import (
	"context"
	"fmt"
	"strings"

	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/internal/mcp"
)

type searchParams struct {
	Query string `json:"query"`
}

func (h *handler) searchTool() *mcp.ServerTool {
	const desc = `Search for symbols in the Go workspace.

Search for symbols using case-insensitive fuzzy search, which may match all or
part of the fully qualified symbol name. For example, the query 'foo' matches
Go symbols 'Foo', 'fooBar', 'futils.Oboe', 'github.com/foo/bar.Baz'.

Results are limited to 100 symbols.
`
	return mcp.NewServerTool(
		"go_search",
		desc,
		h.searchHandler,
		mcp.Input(
			mcp.Property("query", mcp.Description("the fuzzy search query to use for matching symbols")),
		),
	)
}

func (h *handler) searchHandler(ctx context.Context, _ *mcp.ServerSession, params *mcp.CallToolParamsFor[searchParams]) (*mcp.CallToolResultFor[any], error) {
	query := params.Arguments.Query
	if len(query) == 0 {
		return nil, fmt.Errorf("empty query")
	}
	syms, err := h.lspServer.Symbol(ctx, &protocol.WorkspaceSymbolParams{
		Query: params.Arguments.Query,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to execute symbol query: %v", err)
	}
	if len(syms) == 0 {
		return textResult("No symbols found."), nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Top symbol matches:\n")
	for _, sym := range syms {
		fmt.Fprintf(&b, "\t%s (%s in `%s`)\n", sym.Name, kindName(sym.Kind), sym.Location.URI.Path())
	}
	return textResult(b.String()), nil
}

// kindName returns the adjusted name for the given symbol kind,
// fixing LSP conventions that don't work for go, like 'Class'.
func kindName(k protocol.SymbolKind) string {
	if k == protocol.Class {
		return "Type"
	}
	return fmt.Sprint(k)
}
