package lsp

import (
	"context"

	"golang.org/x/tools/internal/lsp/protocol"
	"golang.org/x/tools/internal/lsp/source"
	"golang.org/x/tools/internal/span"
)

func (s *Server) implementation(ctx context.Context, params *protocol.TextDocumentPositionParams) ([]protocol.Location, error) {
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

		locs, err := source.Implementation(ctx, view.Search(), f, rng.Start)
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
