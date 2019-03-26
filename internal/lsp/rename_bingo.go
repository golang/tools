package lsp

import (
	"context"

	"golang.org/x/tools/internal/lsp/protocol"
)

func (s *server) rename(ctx context.Context, params *protocol.RenameParams) ([]protocol.WorkspaceEdit, error) {
	rp := &protocol.ReferenceParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: params.TextDocument,
			Position:     params.Position,
		},
		Context: protocol.ReferenceContext{
			IncludeDeclaration: true,
		},
	}

	references, err := s.references(ctx, rp)
	if err != nil {
		return []protocol.WorkspaceEdit{}, err
	}

	result := protocol.WorkspaceEdit{}
	if result.Changes == nil {
		result.Changes = &map[string][]protocol.TextEdit{}
	}
	for _, ref := range references {
		edit := protocol.TextEdit{
			Range:   ref.Range,
			NewText: params.NewName,
		}
		edits := (*result.Changes)[string(ref.URI)]
		if edits == nil {
			edits = []protocol.TextEdit{}
		}
		edits = append(edits, edit)
		(*result.Changes)[string(ref.URI)] = edits
	}
	return []protocol.WorkspaceEdit{result}, nil
}
