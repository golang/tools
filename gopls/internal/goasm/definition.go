// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package goasm

import (
	"context"
	"go/token"

	"golang.org/x/tools/gopls/internal/cache"
	"golang.org/x/tools/gopls/internal/file"
	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/internal/event"
)

// Definition handles the textDocument/definition request for Go assembly files.
func Definition(ctx context.Context, snapshot *cache.Snapshot, fh file.Handle, rng protocol.Range) ([]protocol.Location, error) {
	ctx, done := event.Start(ctx, "goasm.Definition")
	defer done()

	res, err := resolve(ctx, snapshot, fh, rng)
	if err != nil {
		return nil, err
	}

	// Package-qualified symbol: jump to its Go declaration.
	if res.obj != nil {
		pos := res.obj.Pos()
		pgf, err := res.pkg.FileEnclosing(pos)
		if err != nil {
			return nil, err
		}
		loc, err := pgf.PosLocation(pos, pos+token.Pos(len(res.obj.Name())))
		if err != nil {
			return nil, err
		}
		return []protocol.Location{loc}, nil
	}

	// Local symbol: jump to its definition in the assembly file.
	if res.localDef != nil {
		loc, err := res.file.Mapper.OffsetLocation(res.localDef.Offset, res.localDef.End())
		if err != nil {
			return nil, err
		}
		return []protocol.Location{loc}, nil
	}

	return nil, nil
}
