package lsp

import (
	"context"

	"golang.org/x/tools/internal/lsp/protocol"
	"golang.org/x/tools/internal/lsp/source"
	"golang.org/x/tools/internal/span"
)

func (s *Server) references(ctx context.Context, params *protocol.ReferenceParams) ([]protocol.Location, error) {
	uri := span.NewURI(params.TextDocument.URI)
	view := s.session.ViewOf(uri)
	f, m, err := getGoFile(ctx, view, uri)
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

	// Find all references to the identifier at the position.
	ident, err := source.Identifier(ctx, view, f, rng.Start)
	if err != nil {
		return nil, err
	}
	references, err := ident.References(ctx)
	if err != nil {
		return nil, err
	}

	// Get the location of each reference to return as the result.
	locations := make([]protocol.Location, 0, len(references))
	for _, ref := range references {
		refSpan, err := ref.Range.Span()
		if err != nil {
			return nil, err
		}
		_, refM, err := getSourceFile(ctx, view, refSpan.URI())
		if err != nil {
			return nil, err
		}
		loc, err := refM.Location(refSpan)
		if err != nil {
			return nil, err
		}

		locations = append(locations, loc)
	}
	return locations, nil
}
