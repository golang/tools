// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package completion

import (
	"bytes"
	"context"
	"fmt"

	"golang.org/x/tools/gopls/internal/cache"
	"golang.org/x/tools/gopls/internal/cache/parsego"
	"golang.org/x/tools/gopls/internal/file"
	"golang.org/x/tools/gopls/internal/golang"
	"golang.org/x/tools/gopls/internal/protocol"
)

// NewFile returns a document change to complete an empty go file.
func NewFile(ctx context.Context, snapshot *cache.Snapshot, fh file.Handle) (*protocol.DocumentChange, error) {
	if bs, err := fh.Content(); err != nil || len(bs) != 0 {
		return nil, err
	}
	meta, err := snapshot.NarrowestMetadataForFile(ctx, fh.URI())
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	// Copy the copyright header from the first existing file that has one.
	for _, fileURI := range meta.GoFiles {
		if fileURI == fh.URI() {
			continue
		}
		fh, err := snapshot.ReadFile(ctx, fileURI)
		if err != nil {
			continue
		}
		pgf, err := snapshot.ParseGo(ctx, fh, parsego.Header)
		if err != nil {
			continue
		}
		if group := golang.CopyrightComment(pgf.File); group != nil {
			start, end, err := pgf.NodeOffsets(group)
			if err != nil {
				continue
			}
			buf.Write(pgf.Src[start:end])
			buf.WriteString("\n\n")
			break
		}
	}

	pkgName, err := bestPackage(ctx, snapshot, fh.URI())
	if err != nil {
		return nil, err
	}

	fmt.Fprintf(&buf, "package %s\n", pkgName)
	change := protocol.DocumentChangeEdit(fh, []protocol.TextEdit{{
		Range:   protocol.Range{}, // insert at start of file
		NewText: buf.String(),
	}})

	return &change, nil
}
