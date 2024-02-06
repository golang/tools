// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package cache

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"golang.org/x/mod/modfile"
	"golang.org/x/tools/gopls/internal/file"
	"golang.org/x/tools/gopls/internal/protocol"
)

// TODO(rfindley): now that experimentalWorkspaceModule is gone, this file can
// be massively cleaned up and/or removed.

// goWorkModules returns the URIs of go.mod files named by the go.work file.
func goWorkModules(ctx context.Context, gowork protocol.DocumentURI, fs file.Source) (map[protocol.DocumentURI]unit, error) {
	fh, err := fs.ReadFile(ctx, gowork)
	if err != nil {
		return nil, err // canceled
	}
	content, err := fh.Content()
	if err != nil {
		return nil, err
	}
	filename := gowork.Path()
	dir := filepath.Dir(filename)
	workFile, err := modfile.ParseWork(filename, content, nil)
	if err != nil {
		return nil, fmt.Errorf("parsing go.work: %w", err)
	}
	modFiles := make(map[protocol.DocumentURI]unit)
	for _, use := range workFile.Use {
		modDir := filepath.FromSlash(use.Path)
		if !filepath.IsAbs(modDir) {
			modDir = filepath.Join(dir, modDir)
		}
		modURI := protocol.URIFromPath(filepath.Join(modDir, "go.mod"))
		modFiles[modURI] = unit{}
	}
	return modFiles, nil
}

// isGoMod reports if uri is a go.mod file.
func isGoMod(uri protocol.DocumentURI) bool {
	return filepath.Base(uri.Path()) == "go.mod"
}

// isGoWork reports if uri is a go.work file.
func isGoWork(uri protocol.DocumentURI) bool {
	return filepath.Base(uri.Path()) == "go.work"
}

// fileExists reports whether the file has a Content (which may be empty).
// An overlay exists even if it is not reflected in the file system.
func fileExists(fh file.Handle) bool {
	_, err := fh.Content()
	return err == nil
}

// errExhausted is returned by findModules if the file scan limit is reached.
var errExhausted = errors.New("exhausted")

// Limit go.mod search to 1 million files. As a point of reference,
// Kubernetes has 22K files (as of 2020-11-24).
//
// Note: per golang/go#56496, the previous limit of 1M files was too slow, at
// which point this limit was decreased to 100K.
const fileLimit = 100_000
