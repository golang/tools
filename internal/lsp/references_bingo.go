package lsp

import (
	"context"
	"golang.org/x/tools/internal/lsp/protocol"
	"golang.org/x/tools/internal/lsp/source"
	"golang.org/x/tools/internal/span"
)

func (s *server) references(ctx context.Context, params *protocol.ReferenceParams) ([]protocol.Location, error) {
	locs, err := s.doReferences(ctx, params)
	if err != nil {
		// fix https://github.com/saibing/bingo/issues/32
		params.Position.Character--
		locs, err = s.doReferences(ctx, params)
	}
	return locs, err
}

func (s *server) doReferences(ctx context.Context,  params *protocol.ReferenceParams) ([]protocol.Location, error) {
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

	locs, err := source.References(ctx, s.workspace.Search, f, rng.Start, params.Context.IncludeDeclaration)
	if err != nil {
		return nil, err
	}

	return toProtocolLocations(m, locs), nil
}

func toProtocolLocations(m *protocol.ColumnMapper, locs []source.Location) []protocol.Location {
	if len(locs) == 0 {
		return []protocol.Location{}
	}

	var plocs []protocol.Location
	for _, loc := range locs {
		rng := toProtocolRange(loc.Span)
		ploc := protocol.Location{
			URI: string(loc.Span.URI()),
			Range: rng,
		}
		plocs = append(plocs, ploc)
	}

	return plocs
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

