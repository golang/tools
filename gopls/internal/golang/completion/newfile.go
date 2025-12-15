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

// NewFile returns a document change to complete an empty Go source file. Document change may be nil.
func NewFile(ctx context.Context, snapshot *cache.Snapshot, fh file.Handle) (*protocol.DocumentChange, error) {
	if !snapshot.Options().NewGoFileHeader {
		return nil, nil
	}
	content, err := fh.Content()
	if err != nil {
		return nil, err
	}
	if len(content) != 0 {
		return nil, fmt.Errorf("file is not empty")
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
			text, err := pgf.NodeText(group)
			if err != nil {
				continue
			}
			buf.Write(text)
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
