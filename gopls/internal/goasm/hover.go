// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package goasm

import (
	"context"
	"fmt"

	"golang.org/x/tools/gopls/internal/cache"
	"golang.org/x/tools/gopls/internal/file"
	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/gopls/internal/util/asm"
	"golang.org/x/tools/internal/event"
)

// Hover handles the textDocument/hover request for Go assembly files.
func Hover(ctx context.Context, snapshot *cache.Snapshot, fh file.Handle, rng protocol.Range) (*protocol.Hover, error) {
	ctx, done := event.Start(ctx, "goasm.Hover")
	defer done()

	content, err := fh.Content()
	if err != nil {
		return nil, err
	}

	asmFile := asm.Parse(fh.URI(), content)

	start, end, err := asmFile.Mapper.RangeOffsets(rng)
	if err != nil {
		return nil, err
	}

	// Find the identifier under the cursor.
	var found *asm.Ident
	for _, id := range asmFile.Idents {
		if id.Offset <= start && end <= id.End() {
			found = &id
			break
		}
	}
	if found == nil {
		return nil, nil
	}

	identRange, err := asmFile.IdentRange(*found)
	if err != nil {
		return nil, err
	}

	var hoverText string
	if snapshot.Options().PreferredContentFormat == protocol.Markdown {
		hoverText = fmt.Sprintf("**%s** — %s", found.Name, found.Kind)
	} else {
		hoverText = fmt.Sprintf("%s — %s", found.Name, found.Kind)
	}

	return &protocol.Hover{
		Contents: protocol.MarkupContent{
			Kind:  snapshot.Options().PreferredContentFormat,
			Value: hoverText,
		},
		Range: identRange,
	}, nil
}
