// Copyright 2019 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package server

import (
	"context"

	"golang.org/x/tools/gopls/internal/file"
	"golang.org/x/tools/gopls/internal/golang"
	"golang.org/x/tools/gopls/internal/label"
	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/gopls/internal/template"
	"golang.org/x/tools/internal/event"
)

func (s *server) DocumentSymbol(ctx context.Context, params *protocol.DocumentSymbolParams) ([]any, error) {
	ctx, done := event.Start(ctx, "lsp.Server.documentSymbol", label.URI.Of(params.TextDocument.URI))
	defer done()

	fh, snapshot, release, err := s.fileOf(ctx, params.TextDocument.URI)
	if err != nil {
		return nil, err
	}
	defer release()

	var docSymbols []protocol.DocumentSymbol
	switch snapshot.FileKind(fh) {
	case file.Tmpl:
		docSymbols, err = template.DocumentSymbols(snapshot, fh)
	case file.Go:
		docSymbols, err = golang.DocumentSymbols(ctx, snapshot, fh)
	default:
		return nil, nil // empty result
	}
	if err != nil {
		event.Error(ctx, "DocumentSymbols failed", err)
		return nil, nil // empty result
	}
	// Convert the symbols to an interface array.
	// TODO: Remove this once the lsp deprecates SymbolInformation.
	symbols := make([]any, len(docSymbols))
	for i, s := range docSymbols {
		if snapshot.Options().HierarchicalDocumentSymbolSupport {
			symbols[i] = s
			continue
		}
		// If the client does not support hierarchical document symbols, then
		// we need to be backwards compatible for now and return SymbolInformation.
		symbols[i] = protocol.SymbolInformation{
			Name:       s.Name,
			Kind:       s.Kind,
			Deprecated: s.Deprecated,
			Location: protocol.Location{
				URI:   params.TextDocument.URI,
				Range: s.Range,
			},
		}
	}
	return symbols, nil
}
