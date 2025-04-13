// Copyright 2019 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package cache

import (
	"context"
	"fmt"
	"go/parser"
	"go/token"
	"path/filepath"

	"golang.org/x/tools/gopls/internal/cache/parsego"
	"golang.org/x/tools/gopls/internal/file"
	"golang.org/x/tools/gopls/internal/util/asm"
)

// ParseGo parses the file whose contents are provided by fh.
// The resulting tree may have been fixed up.
// If the file is not available, returns nil and an error.
func (s *Snapshot) ParseGo(ctx context.Context, fh file.Handle, mode parser.Mode) (*parsego.File, error) {
	pgfs, err := s.view.parseCache.parseFiles(ctx, token.NewFileSet(), mode, false, fh)
	if err != nil {
		return nil, err
	}
	return pgfs[0], nil
}

// parseGoImpl parses the Go source file whose content is provided by fh.
func parseGoImpl(ctx context.Context, fset *token.FileSet, fh file.Handle, mode parser.Mode, purgeFuncBodies bool) (*parsego.File, error) {
	ext := filepath.Ext(fh.URI().Path())
	if ext != ".go" && ext != "" { // files generated by cgo have no extension
		return nil, fmt.Errorf("cannot parse non-Go file %s", fh.URI())
	}
	content, err := fh.Content()
	if err != nil {
		return nil, err
	}
	// Check for context cancellation before actually doing the parse.
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	pgf, _ := parsego.Parse(ctx, fset, fh.URI(), content, mode, purgeFuncBodies) // ignore 'fixes'
	return pgf, nil
}

func parseAsmFile(fhs ...file.Handle) ([]*asm.File, error) {
	pgfs := make([]*asm.File, len(fhs))

	// Temporary fall-back for 32-bit systems, where reservedForParsing is too
	// small to be viable. We don't actually support 32-bit systems, so this
	// workaround is only for tests and can be removed when we stop running
	// 32-bit TryBots for gopls.
	for i, fh := range fhs {
		var err error
		content, err := fh.Content()
		if err != nil {
			return nil, err
		}
		pgfs[i] = asm.Parse(content)
	}
	return pgfs, nil

}
