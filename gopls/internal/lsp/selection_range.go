package lsp

import (
	"context"
	"fmt"

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

	if len(params.Positions) != 2 {
		return nil, fmt.Errorf("expected 2 positions, received %d", len(params.Positions))
	}

	start, err := pgf.Mapper.Pos(params.Positions[0])
	if err != nil {
		return nil, err
	}

	end, err := pgf.Mapper.Pos(params.Positions[1])
	if err != nil {
		return nil, err
	}

	path, _ := astutil.PathEnclosingInterval(pgf.File, start, end)

	n := path[0]
	if len(path) >= 2 && n.Pos() == start && n.End() == end {
		n = path[1]
	}

	newSelection, err := pgf.Mapper.PosRange(n.Pos(), n.End())
	if err != nil {
		return nil, err
	}

	return []protocol.SelectionRange{{Range: newSelection}}, nil
}
