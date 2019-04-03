package lsp

import (
	"context"

	"golang.org/x/tools/internal/lsp/protocol"
	"golang.org/x/tools/internal/lsp/source"
	"golang.org/x/tools/internal/span"
)

func (s *Server) implementation(ctx context.Context, params *protocol.TextDocumentPositionParams) ([]protocol.Location, error) {
	var locations []source.Location
	for i := range s.views {
		f, m, err := newColumnMap(ctx, s.views[i], span.URI(params.TextDocument.URI))
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

		locs, err := source.Implementation(ctx, s.workspaces[i].Search, f, rng.Start)
		if err != nil {
			return nil, err
		}
		locations = append(locations, locs...)
	}
	return toProtocolLocations(locations), nil
}
