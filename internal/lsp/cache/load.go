// Copyright 2019 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package cache

import (
	"context"
	"fmt"
	"go/types"
	"sort"
	"strings"

	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/internal/lsp/telemetry"
	"golang.org/x/tools/internal/packagesinternal"
	"golang.org/x/tools/internal/span"
	"golang.org/x/tools/internal/telemetry/log"
	"golang.org/x/tools/internal/telemetry/tag"
	"golang.org/x/tools/internal/telemetry/trace"
	errors "golang.org/x/xerrors"
)

type metadata struct {
	id              packageID
	pkgPath         packagePath
	name            string
	goFiles         []span.URI
	compiledGoFiles []span.URI
	forTest         packagePath
	typesSizes      types.Sizes
	errors          []packages.Error
	deps            []packageID
	missingDeps     map[packagePath]struct{}

	// config is the *packages.Config associated with the loaded package.
	config *packages.Config
}

func (s *snapshot) load(ctx context.Context, scopes ...interface{}) ([]*metadata, error) {
	var query []string
	var containsDir bool // for logging
	for _, scope := range scopes {
		switch scope := scope.(type) {
		case packagePath:
			// The only time we pass package paths is when we're doing a
			// partial workspace load. In those cases, the paths came back from
			// go list and should already be GOPATH-vendorized when appropriate.
			query = append(query, string(scope))
		case fileURI:
			query = append(query, fmt.Sprintf("file=%s", span.URI(scope).Filename()))
		case directoryURI:
			filename := span.URI(scope).Filename()
			q := fmt.Sprintf("%s/...", filename)
			// Simplify the query if it will be run in the requested directory.
			// This ensures compatibility with Go 1.12 that doesn't allow
			// <directory>/... in GOPATH mode.
			if s.view.folder.Filename() == filename {
				q = "./..."
			}
			query = append(query, q)
		case viewLoadScope:
			// If we are outside of GOPATH, a module, or some other known
			// build system, don't load subdirectories.
			if !s.view.hasValidBuildConfiguration {
				query = append(query, "./")
			} else {
				query = append(query, "./...")
			}
		default:
			panic(fmt.Sprintf("unknown scope type %T", scope))
		}
		switch scope.(type) {
		case directoryURI, viewLoadScope:
			containsDir = true
		}
	}
	sort.Strings(query) // for determinism

	ctx, done := trace.StartSpan(ctx, "cache.view.load", telemetry.Query.Of(query))
	defer done()

	cfg := s.view.Config(ctx)
	pkgs, err := s.view.loadPackages(cfg, query...)

	// If the context was canceled, return early. Otherwise, we might be
	// type-checking an incomplete result. Check the context directly,
	// because go/packages adds extra information to the error.
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	log.Print(ctx, "go/packages.Load", tag.Of("snapshot", s.ID()), tag.Of("query", query), tag.Of("packages", len(pkgs)))
	if len(pkgs) == 0 {
		return nil, err
	}
	var results []*metadata
	for _, pkg := range pkgs {
		if !containsDir || s.view.Options().VerboseOutput {
			log.Print(ctx, "go/packages.Load", tag.Of("snapshot", s.ID()), tag.Of("package", pkg.PkgPath), tag.Of("files", pkg.CompiledGoFiles))
		}
		// Ignore packages with no sources, since we will never be able to
		// correctly invalidate that metadata.
		if len(pkg.GoFiles) == 0 && len(pkg.CompiledGoFiles) == 0 {
			continue
		}
		// Skip test main packages.
		if isTestMain(ctx, pkg, s.view.gocache) {
			continue
		}
		// Set the metadata for this package.
		m, err := s.setMetadata(ctx, packagePath(pkg.PkgPath), pkg, cfg, map[packageID]struct{}{})
		if err != nil {
			return nil, err
		}
		// All packages returned by packages.Load will be top-level packages,
		// with dependencies in the Imports field. Therefore, we can assume that
		// they are all workspace packages and mark them as such.
		if err := s.setWorkspacePackage(ctx, m); err != nil {
			return nil, err
		}
		results = append(results, m)
	}

	// Rebuild the import graph when the metadata is updated.
	s.clearAndRebuildImportGraph()

	if len(results) == 0 {
		return nil, errors.Errorf("no metadata for %s", scopes)
	}
	return results, nil
}

func (s *snapshot) setWorkspacePackage(ctx context.Context, m *metadata) error {
	// Make sure that the builtin package doesn't get marked a workspace package.
	if m.pkgPath == "builtin" {
		return nil
	}
	// A test variant of a package can only be loaded directly by loading
	// the non-test variant with -test. Track the import path of the non-test variant.
	pkgPath := m.pkgPath
	if m.forTest != "" {
		pkgPath = m.forTest
	}

	s.mu.Lock()
	s.workspacePackages[m.id] = pkgPath
	s.mu.Unlock()

	_, err := s.packageHandle(ctx, m.id)
	return err
}

func (s *snapshot) setMetadata(ctx context.Context, pkgPath packagePath, pkg *packages.Package, cfg *packages.Config, seen map[packageID]struct{}) (*metadata, error) {
	id := packageID(pkg.ID)
	if _, ok := seen[id]; ok {
		return nil, errors.Errorf("import cycle detected: %q", id)
	}
	// Recreate the metadata rather than reusing it to avoid locking.
	m := &metadata{
		id:         id,
		pkgPath:    pkgPath,
		name:       pkg.Name,
		forTest:    packagePath(packagesinternal.GetForTest(pkg)),
		typesSizes: pkg.TypesSizes,
		errors:     pkg.Errors,
		config:     cfg,
	}

	for _, filename := range pkg.CompiledGoFiles {
		uri := span.FileURI(filename)
		m.compiledGoFiles = append(m.compiledGoFiles, uri)
		s.addID(uri, m.id)
	}
	for _, filename := range pkg.GoFiles {
		uri := span.FileURI(filename)
		m.goFiles = append(m.goFiles, uri)
		s.addID(uri, m.id)
	}

	copied := map[packageID]struct{}{
		id: struct{}{},
	}
	for k, v := range seen {
		copied[k] = v
	}
	for importPath, importPkg := range pkg.Imports {
		importPkgPath := packagePath(importPath)
		importID := packageID(importPkg.ID)

		m.deps = append(m.deps, importID)

		// Don't remember any imports with significant errors.
		if importPkgPath != "unsafe" && len(importPkg.CompiledGoFiles) == 0 {
			if m.missingDeps == nil {
				m.missingDeps = make(map[packagePath]struct{})
			}
			m.missingDeps[importPkgPath] = struct{}{}
			continue
		}
		if s.getMetadata(importID) == nil {
			if _, err := s.setMetadata(ctx, importPkgPath, importPkg, cfg, copied); err != nil {
				log.Error(ctx, "error in dependency", err)
			}
		}
	}
	// Add the metadata to the cache.
	s.mu.Lock()
	defer s.mu.Unlock()

	// TODO: We should make sure not to set duplicate metadata,
	// and instead panic here. This can be done by making sure not to
	// reset metadata information for packages we've already seen.
	if orig, ok := s.metadata[m.id]; ok {
		return orig, nil
	} else {
		s.metadata[m.id] = m
		return m, nil
	}
}

func isTestMain(ctx context.Context, pkg *packages.Package, gocache string) bool {
	// Test mains must have an import path that ends with ".test".
	if !strings.HasSuffix(pkg.PkgPath, ".test") {
		return false
	}
	// Test main packages are always named "main".
	if pkg.Name != "main" {
		return false
	}
	// Test mains always have exactly one GoFile that is in the build cache.
	if len(pkg.GoFiles) > 1 {
		return false
	}
	if !strings.HasPrefix(pkg.GoFiles[0], gocache) {
		return false
	}
	return true
}
