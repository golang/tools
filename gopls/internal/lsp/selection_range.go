package lsp

import (
	"context"

	"golang.org/x/tools/go/ast/astutil"
	"golang.org/x/tools/gopls/internal/lsp/protocol"
	"golang.org/x/tools/gopls/internal/lsp/source"
	"golang.org/x/tools/internal/event"
)

func (s *Server) selectionRange(ctx context.Context, params *protocol.SelectionRangeParams) ([]protocol.SelectionRange, error) {
	ctx, done := event.Start(ctx, "lsp.Server.documentSymbol")
	defer done()

	snapshot, fh, ok, release, err := s.beginFileRequest(ctx, params.TextDocument.URI, source.UnknownKind)
	defer release()
	if !ok {
		return nil, err
	}

	pgf, err := snapshot.ParseGo(ctx, fh, source.ParseFull)
	if err != nil {
		return nil, err
	}

	result := make([]protocol.SelectionRange, len(params.Positions))
	for i, protocolPos := range params.Positions {
		pos, err := pgf.Mapper.Pos(protocolPos)
		if err != nil {
			return nil, err
		}

		path, _ := astutil.PathEnclosingInterval(pgf.File, pos, pos)

		current := &result[i]

		for j, node := range path {
			rng, err := pgf.Mapper.PosRange(node.Pos(), node.End())
			if err != nil {
				return nil, err
			}

			// Option 2
			current.Range = rng

			if j < len(path)-1 {
				current.Parent = &protocol.SelectionRange{}
				current = current.Parent
			}
		}
	}

	return result, nil
}
