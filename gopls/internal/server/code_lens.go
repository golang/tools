// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package server

import (
	"context"
	"fmt"
	"sort"

	"golang.org/x/tools/gopls/internal/cache"
	"golang.org/x/tools/gopls/internal/file"
	"golang.org/x/tools/gopls/internal/golang"
	"golang.org/x/tools/gopls/internal/label"
	"golang.org/x/tools/gopls/internal/mod"
	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/gopls/internal/settings"
	"golang.org/x/tools/internal/event"
)

// CodeLens reports the set of available CodeLenses
// (range-associated commands) in the given file.
func (s *server) CodeLens(ctx context.Context, params *protocol.CodeLensParams) ([]protocol.CodeLens, error) {
	ctx, done := event.Start(ctx, "lsp.Server.codeLens", label.URI.Of(params.TextDocument.URI))
	defer done()

	fh, snapshot, release, err := s.fileOf(ctx, params.TextDocument.URI)
	if err != nil {
		return nil, err
	}
	defer release()

	var lensFuncs map[settings.CodeLensSource]cache.CodeLensSourceFunc
	switch snapshot.FileKind(fh) {
	case file.Mod:
		lensFuncs = mod.CodeLensSources()
	case file.Go:
		lensFuncs = golang.CodeLensSources()
	default:
		// Unsupported file kind for a code lens.
		return nil, nil
	}
	var lenses []protocol.CodeLens
	for kind, lensFunc := range lensFuncs {
		if !snapshot.Options().Codelenses[kind] {
			continue
		}
		added, err := lensFunc(ctx, snapshot, fh)
		// Code lens is called on every keystroke, so we should just operate in
		// a best-effort mode, ignoring errors.
		if err != nil {
			event.Error(ctx, fmt.Sprintf("code lens %s failed", kind), err)
			continue
		}
		lenses = append(lenses, added...)
	}
	sort.Slice(lenses, func(i, j int) bool {
		a, b := lenses[i], lenses[j]
		if cmp := protocol.CompareRange(a.Range, b.Range); cmp != 0 {
			return cmp < 0
		}
		return a.Command.Command < b.Command.Command
	})
	return lenses, nil
}
