package lsp

import (
	"context"

	"golang.org/x/tools/internal/lsp/protocol"
	"golang.org/x/tools/internal/lsp/source"
	"golang.org/x/tools/internal/span"
)

func (s *Server) referencesBingo(ctx context.Context, params *protocol.ReferenceParams) ([]protocol.Location, error) {
	locations, err := s.doReferences(ctx, params)
	if err != nil {
		// fix https://github.com/saibing/bingo/issues/32
		params.Position.Character--
		locations, err = s.doReferences(ctx, params)
	}
	return locations, err
}

func (s *Server) doReferences(ctx context.Context, params *protocol.ReferenceParams) (locations []protocol.Location, err error) {
	f := func(view source.View) error {
		f, err := getGoFile(ctx, view, span.URI(params.TextDocument.URI))
		if err != nil {
			return err
		}

		m, err := getMapper(ctx, f)
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

		refers, err := source.References(ctx, view.Search(), f, rng.Start, params.Context.IncludeDeclaration)
		if err != nil {
			return err
		}

		locs, err := toProtocolLocations(ctx, view, refers)
		if err != nil {
			return err
		}

		locations = append(locations, locs...)
		return nil
	}

	err = walkSession(s.session, f)
	return
}

func toProtocolLocations(ctx context.Context, view source.View, references []*source.ReferenceInfo) ([]protocol.Location, error) {
	// Get the location of each reference to return as the result.
	locations := make([]protocol.Location, 0, len(references))
	seen := make(map[span.Span]bool)
	for _, ref := range references {
		refSpan, err := ref.Span()
		if err != nil {
			return nil, err
		}
		if seen[refSpan] {
			continue // already added this location
		}
		seen[refSpan] = true

		refFile, err := getGoFile(ctx, view, refSpan.URI())
		if err != nil {
			return nil, err
		}
		refM, err := getMapper(ctx, refFile)
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
