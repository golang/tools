// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package server

import (
	"context"
	"fmt"
	"slices"

	"golang.org/x/tools/gopls/internal/cache"
	"golang.org/x/tools/gopls/internal/cache/symbols"
	"golang.org/x/tools/gopls/internal/golang"
	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/gopls/internal/settings"
	"golang.org/x/tools/internal/event"
)

// InteractiveWorkspaceSymbolEnumConfig defines the JSON configuration payload
// for an InteractiveListEnum request when the source is "workspaceSymbol".
// It is used to filter the search results to specific symbol types (e.g.,
// only Interfaces).
type InteractiveWorkspaceSymbolEnumConfig struct {
	Kinds []protocol.SymbolKind
}

// InteractiveListEnum handles requests to dynamically populate interactive UI
// elements in the client. Based on the requested param.Source, it queries the
// underlying session data (like workspace symbols) and returns a list of enum
// entries matching the user's query.
func (s *server) InteractiveListEnum(ctx context.Context, param *protocol.InteractiveListEnumParams) ([]protocol.FormEnumEntry, error) {
	ctx, done := event.Start(ctx, "server.interactiveListEnum")
	defer done()
	switch param.Source {
	case "workspaceSymbol":
		var config InteractiveWorkspaceSymbolEnumConfig
		if err := protocol.UnmarshalJSON(param.Config, &config); err != nil {
			return nil, fmt.Errorf("unmarshalling InteractiveListEnumParams.Config: %v", err)
		}

		var snapshots []*cache.Snapshot
		for _, v := range s.session.Views() {
			snapshot, release, err := v.Snapshot()
			if err != nil {
				continue // snapshot is shutting down
			}
			// If err is non-nil, the snapshot is shutting down. Skip it.
			defer release()
			snapshots = append(snapshots, snapshot)
		}

		symbols, err := golang.WorkspaceSymbols(ctx, snapshots, param.Query,
			golang.WorkspaceSymbolsOptions{
				// Use FastFuzzy for low-latency interactive matching.
				Matcher: settings.SymbolFastFuzzy,

				// Use fully qualified names to ensure unique identification of
				// the symbol. (i.e. "path/to/pkg.Iface")
				Style: settings.FullyQualifiedSymbols,
				Filter: golang.SymbolFilter(func(sym symbols.Symbol) bool {
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
				Description: fmt.Sprintf("[%v] %s", s.Kind, s.Name),
			})
		}
		return entries, nil
	default:
		return nil, fmt.Errorf("unrecognized enum source: %s", param.Source)
	}
}
