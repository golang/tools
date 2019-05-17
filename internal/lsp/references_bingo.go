package lsp

import (
	"context"

	"golang.org/x/tools/internal/lsp/protocol"
	"golang.org/x/tools/internal/lsp/source"
	"golang.org/x/tools/internal/span"
)

func (s *Server) references(ctx context.Context, params *protocol.ReferenceParams) ([]protocol.Location, error) {
	locations, err := s.doReferences(ctx, params)
	if err != nil {
		// fix https://github.com/saibing/bingo/issues/32
		params.Position.Character--
		locations, err = s.doReferences(ctx, params)
	}
	return locations, err
}

func (s *Server) doReferences(ctx context.Context, params *protocol.ReferenceParams) ([]protocol.Location, error) {
	var locations []source.Location

	f := func(view source.View) error {
		f, m, err := getGoFile(ctx, view, span.URI(params.TextDocument.URI))
		if err != nil {
			return err
		}
		spn, err := m.PointSpan(params.Position)
		if err != nil {
			return err
		}
		rng, err := spn.Range(m.Converter)
		if err != nil {
			return err
		}

		locs, err := source.References(ctx, view.Search(), f, rng.Start, params.Context.IncludeDeclaration)
		if err != nil {
			return err
		}
		locations = append(locations, locs...)
		return nil
	}

	err := walkSession(s.session, f)
	if err != nil {
		return nil, err
	}

	return toProtocolLocations(locations), nil
}

func toProtocolLocations(locations []source.Location) []protocol.Location {
	if len(locations) == 0 {
		return []protocol.Location{}
	}

	var pLocations []protocol.Location
	for _, loc := range locations {
		rng := toProtocolRange(loc.Span)
		ploc := protocol.Location{
			URI:   string(loc.Span.URI()),
			Range: rng,
		}
		pLocations = append(pLocations, ploc)
	}

	return pLocations
}

func toProtocolRange(spn span.Span) protocol.Range {
	var rng protocol.Range

	rng.Start = toProtocolPosition(spn.Start())
	rng.End = toProtocolPosition(spn.End())

	return rng
}

func toProtocolPosition(point span.Point) protocol.Position {
	var pos protocol.Position
	pos.Line = float64(point.Line() - 1)
	pos.Character = float64(point.Column() - 1)

	return pos
}
