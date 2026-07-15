// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package golang

import (
	"context"

	"golang.org/x/tools/gopls/internal/cache"
	"golang.org/x/tools/gopls/internal/file"
	"golang.org/x/tools/gopls/internal/protocol"
)

func MoveDeclaration(ctx context.Context, fh file.Handle, snapshot *cache.Snapshot) ([]protocol.DocumentChange, protocol.Location, error) {
	return nil, protocol.Location{}, nil
}
