package lsp

import (
	"context"
	"fmt"

	"golang.org/x/tools/internal/lsp/protocol"
	"golang.org/x/tools/internal/lsp/source"
	"golang.org/x/tools/internal/span"
)

func (s *Server) executeCommand(ctx context.Context, params *protocol.ExecuteCommandParams) (interface{}, error) {
	switch params.Command {
	case "tidy":
		if len(params.Arguments) == 0 || len(params.Arguments) > 1 {
			return nil, fmt.Errorf("expected one file URI for call to `go mod tidy`, got %v", params.Arguments)
		}
		// Confirm that this action is being taken on a go.mod file.
		uri := span.NewURI(params.Arguments[0].(string))
		view, err := s.session.ViewOf(uri)
		if err != nil {
			return nil, err
		}
		f, err := view.GetFile(ctx, uri)
		if err != nil {
			return nil, err
		}
		fh := view.Snapshot().Handle(ctx, f)
		if fh.Identity().Kind != source.Mod {
			return nil, fmt.Errorf("%s is not a mod file", uri)
		}
		// Run go.mod tidy on the view.
		if err := source.ModTidy(ctx, view); err != nil {
			return nil, err
		}
	}
	return nil, nil
}
