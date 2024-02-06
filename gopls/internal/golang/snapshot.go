// Copyright 2018 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package golang

import (
	"context"
	"fmt"

	"golang.org/x/tools/gopls/internal/cache"
	"golang.org/x/tools/gopls/internal/cache/metadata"
	"golang.org/x/tools/gopls/internal/cache/parsego"
	"golang.org/x/tools/gopls/internal/protocol"
)

// NarrowestMetadataForFile returns metadata for the narrowest package
// (the one with the fewest files) that encloses the specified file.
// The result may be a test variant, but never an intermediate test variant.
func NarrowestMetadataForFile(ctx context.Context, snapshot *cache.Snapshot, uri protocol.DocumentURI) (*metadata.Package, error) {
	mps, err := snapshot.MetadataForFile(ctx, uri)
	if err != nil {
		return nil, err
	}
	metadata.RemoveIntermediateTestVariants(&mps)
	if len(mps) == 0 {
		return nil, fmt.Errorf("no package metadata for file %s", uri)
	}
	return mps[0], nil
}

// NarrowestPackageForFile is a convenience function that selects the narrowest
// non-ITV package to which this file belongs, type-checks it in the requested
// mode (full or workspace), and returns it, along with the parse tree of that
// file.
//
// The "narrowest" package is the one with the fewest number of files that
// includes the given file. This solves the problem of test variants, as the
// test will have more files than the non-test package.
//
// An intermediate test variant (ITV) package has identical source to a regular
// package but resolves imports differently. gopls should never need to
// type-check them.
//
// Type-checking is expensive. Call snapshot.ParseGo if all you need is a parse
// tree, or snapshot.MetadataForFile if you only need metadata.
func NarrowestPackageForFile(ctx context.Context, snapshot *cache.Snapshot, uri protocol.DocumentURI) (*cache.Package, *ParsedGoFile, error) {
	return selectPackageForFile(ctx, snapshot, uri, func(metas []*metadata.Package) *metadata.Package { return metas[0] })
}

// WidestPackageForFile is a convenience function that selects the widest
// non-ITV package to which this file belongs, type-checks it in the requested
// mode (full or workspace), and returns it, along with the parse tree of that
// file.
//
// The "widest" package is the one with the most number of files that includes
// the given file. Which is the test variant if one exists.
//
// An intermediate test variant (ITV) package has identical source to a regular
// package but resolves imports differently. gopls should never need to
// type-check them.
//
// Type-checking is expensive. Call snapshot.ParseGo if all you need is a parse
// tree, or snapshot.MetadataForFile if you only need metadata.
func WidestPackageForFile(ctx context.Context, snapshot *cache.Snapshot, uri protocol.DocumentURI) (*cache.Package, *ParsedGoFile, error) {
	return selectPackageForFile(ctx, snapshot, uri, func(metas []*metadata.Package) *metadata.Package { return metas[len(metas)-1] })
}

func selectPackageForFile(ctx context.Context, snapshot *cache.Snapshot, uri protocol.DocumentURI, selector func([]*metadata.Package) *metadata.Package) (*cache.Package, *ParsedGoFile, error) {
	mps, err := snapshot.MetadataForFile(ctx, uri)
	if err != nil {
		return nil, nil, err
	}
	metadata.RemoveIntermediateTestVariants(&mps)
	if len(mps) == 0 {
		return nil, nil, fmt.Errorf("no package metadata for file %s", uri)
	}
	mp := selector(mps)
	pkgs, err := snapshot.TypeCheck(ctx, mp.ID)
	if err != nil {
		return nil, nil, err
	}
	pkg := pkgs[0]
	pgf, err := pkg.File(uri)
	if err != nil {
		return nil, nil, err // "can't happen"
	}
	return pkg, pgf, err
}

type ParsedGoFile = parsego.File

const (
	ParseHeader = parsego.ParseHeader
	ParseFull   = parsego.ParseFull
)

type (
	PackageID   = metadata.PackageID
	PackagePath = metadata.PackagePath
	PackageName = metadata.PackageName
	ImportPath  = metadata.ImportPath
)

type unit = struct{}
