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
	"golang.org/x/tools/gopls/internal/test/integration/fake/glob"
)

// isGoWork reports if uri is a go.work file.
func isGoWork(uri protocol.DocumentURI) bool {
	return filepath.Base(uri.Path()) == "go.work"
}

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
	var usedDirs []string
	for _, use := range workFile.Use {
		usedDirs = append(usedDirs, use.Path)
	}
	return localModFiles(dir, usedDirs), nil
}

// localModFiles builds a set of local go.mod files referenced by
// goWorkOrModPaths, which is a slice of paths as contained in a go.work 'use'
// directive or go.mod 'replace' directive (and which therefore may use either
// '/' or '\' as a path separator).
func localModFiles(relativeTo string, goWorkOrModPaths []string) map[protocol.DocumentURI]unit {
	modFiles := make(map[protocol.DocumentURI]unit)
	for _, path := range goWorkOrModPaths {
		modDir := filepath.FromSlash(path)
		if !filepath.IsAbs(modDir) {
			modDir = filepath.Join(relativeTo, modDir)
		}
		modURI := protocol.URIFromPath(filepath.Join(modDir, "go.mod"))
		modFiles[modURI] = unit{}
	}
	return modFiles
}

// isGoMod reports if uri is a go.mod file.
func isGoMod(uri protocol.DocumentURI) bool {
	return filepath.Base(uri.Path()) == "go.mod"
}

// isWorkspaceFile reports if uri matches a set of globs defined in workspaceFiles
func isWorkspaceFile(uri protocol.DocumentURI, workspaceFiles []string) bool {
	for _, workspaceFile := range workspaceFiles {
		g, err := glob.Parse(workspaceFile)
		if err != nil {
			continue
		}

		if g.Match(uri.Path()) {
			return true
		}
	}
	return false
}

// goModModules returns the URIs of "workspace" go.mod files defined by a
// go.mod file. This set is defined to be the given go.mod file itself, as well
// as the modfiles of any locally replaced modules in the go.mod file.
func goModModules(ctx context.Context, gomod protocol.DocumentURI, fs file.Source) (map[protocol.DocumentURI]unit, error) {
	fh, err := fs.ReadFile(ctx, gomod)
	if err != nil {
		return nil, err // canceled
	}
	content, err := fh.Content()
	if err != nil {
		return nil, err
	}
	filename := gomod.Path()
	dir := filepath.Dir(filename)
	modFile, err := modfile.Parse(filename, content, nil)
	if err != nil {
		return nil, err
	}
	var localReplaces []string
	for _, replace := range modFile.Replace {
		if modfile.IsDirectoryPath(replace.New.Path) {
			localReplaces = append(localReplaces, replace.New.Path)
		}
	}
	modFiles := localModFiles(dir, localReplaces)
	modFiles[gomod] = unit{}
	return modFiles, nil
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
