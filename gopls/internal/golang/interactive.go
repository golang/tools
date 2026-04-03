// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package golang

import (
	"context"
	"fmt"
	"slices"

	"golang.org/x/tools/gopls/internal/cache"
	"golang.org/x/tools/gopls/internal/cache/symbols"
	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/gopls/internal/settings"
)

// InteractiveWorkspaceSymbolEnumConfig defines the JSON configuration payload
// for an InteractiveListEnum request when the source is "workspaceSymbol".
// It is used to filter the search results to specific symbol types (e.g.,
// only Interfaces).
type InteractiveWorkspaceSymbolEnumConfig struct {
	Kinds []protocol.SymbolKind
}

func ListWorkspaceSymbol(ctx context.Context, snapshots []*cache.Snapshot, param *protocol.InteractiveListEnumParams) ([]protocol.FormEnumEntry, error) {
	var config InteractiveWorkspaceSymbolEnumConfig
	if err := protocol.UnmarshalJSON(param.Config, &config); err != nil {
		return nil, fmt.Errorf("unmarshalling InteractiveListEnumParams.Config: %v", err)
	}

	symbols, err := WorkspaceSymbols(ctx, snapshots, param.Query,
		WorkspaceSymbolsOptions{
			// Use FastFuzzy for low-latency interactive matching.
			Matcher: settings.SymbolFastFuzzy,

			// Use fully qualified names to ensure unique identification of
			// the symbol. (i.e. "path/to/pkg.Iface")
			Style: settings.FullyQualifiedSymbols,
			Filter: SymbolFilter(func(sym symbols.Symbol) bool {
				return slices.Contains(config.Kinds, sym.Kind)
			}),
		},
	)
	if err != nil {
		return nil, err
	}

	entries := make([]protocol.FormEnumEntry, 0, len(symbols))
	for _, s := range symbols {
		entries = append(entries, protocol.FormEnumEntry{
			Value:       s.Name,
			Description: fmt.Sprintf("[%s] %s", s.Kind, s.Name),
		})
	}
	return entries, nil
}
