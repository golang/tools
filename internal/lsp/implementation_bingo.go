package lsp

import (
	"context"

	"golang.org/x/tools/internal/lsp/protocol"
	"golang.org/x/tools/internal/lsp/source"
	"golang.org/x/tools/internal/span"
)

func (s *Server) implementation(ctx context.Context, params *protocol.TextDocumentPositionParams) (locations []protocol.Location, err error) {
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

		refers, err := source.Implementation(ctx, view.Search(), f, rng.Start)
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

type viewWalkFunc func(v source.View) error

func walkSession(session source.Session, f viewWalkFunc) error {
	for _, view := range session.Views() {
		err := f(view)
		if err != nil {
			return err
		}
	}
	return nil
}
