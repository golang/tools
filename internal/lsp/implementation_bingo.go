package lsp

import (
	"context"
	"golang.org/x/tools/internal/lsp/source"
	"golang.org/x/tools/internal/span"

	"golang.org/x/tools/internal/lsp/protocol"
)

func (s *Server) implementation(ctx context.Context, params *protocol.TextDocumentPositionParams) ([]protocol.Location, error) {
	f, m, err := newColumnMap(ctx, s.view, span.URI(params.TextDocument.URI))
	if err != nil {
		return nil, err
	}
	spn, err := m.PointSpan(params.Position)
	if err != nil {
		return nil, err
	}
	rng, err := spn.Range(m.Converter)
	if err != nil {
		return nil, err
	}

	locations, err := source.Implementation(ctx, s.workspace.Search, f, rng.Start)
	if err != nil {
		return nil, err
	}
	return toProtocolLocations(locations), nil
}
