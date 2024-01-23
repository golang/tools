// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// The metadata package defines types and functions for working with package
// metadata, which describes Go packages and their relationships.
//
// Package metadata is loaded by gopls using go/packages, and the [Package]
// type is itself a projection and translation of data from
// go/packages.Package.
//
// Packages are assembled into an immutable [Graph]
package metadata

import (
	"go/ast"
	"go/types"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/gopls/internal/lsp/protocol"
	"golang.org/x/tools/internal/packagesinternal"
)

// Declare explicit types for package paths, names, and IDs to ensure that we
// never use an ID where a path belongs, and vice versa. If we confused these,
// it would result in confusing errors because package IDs often look like
// package paths.
type (
	PackageID   string // go list's unique identifier for a package (e.g. "vendor/example.com/foo [vendor/example.com/bar.test]")
	PackagePath string // name used to prefix linker symbols (e.g. "vendor/example.com/foo")
	PackageName string // identifier in 'package' declaration (e.g. "foo")
	ImportPath  string // path that appears in an import declaration (e.g. "example.com/foo")
)

// Package represents package metadata retrieved from go/packages.
// The DepsBy{Imp,Pkg}Path maps do not contain self-import edges.
//
// An ad-hoc package (without go.mod or GOPATH) has its ID, PkgPath,
// and LoadDir equal to the absolute path of its directory.
type Package struct {
	ID      PackageID
	PkgPath PackagePath
	Name    PackageName

	// these three fields are as defined by go/packages.Package
	GoFiles         []protocol.DocumentURI
	CompiledGoFiles []protocol.DocumentURI
	IgnoredFiles    []protocol.DocumentURI

	ForTest       PackagePath // q in a "p [q.test]" package, else ""
	TypesSizes    types.Sizes
	Errors        []packages.Error          // must be set for packages in import cycles
	DepsByImpPath map[ImportPath]PackageID  // may contain dups; empty ID => missing
	DepsByPkgPath map[PackagePath]PackageID // values are unique and non-empty
	Module        *packages.Module
	DepsErrors    []*packagesinternal.PackageError
	LoadDir       string // directory from which go/packages was run
	Standalone    bool   // package synthesized for a standalone file (e.g. ignore-tagged)
}

func (mp *Package) String() string { return string(mp.ID) }

// IsIntermediateTestVariant reports whether the given package is an
// intermediate test variant (ITV), e.g. "net/http [net/url.test]".
//
// An ITV has identical syntax to the regular variant, but different
// import metadata (DepsBy{Imp,Pkg}Path).
//
// Such test variants arise when an x_test package (in this case net/url_test)
// imports a package (in this case net/http) that itself imports the
// non-x_test package (in this case net/url).
//
// This is done so that the forward transitive closure of net/url_test has
// only one package for the "net/url" import.
// The ITV exists to hold the test variant import:
//
// net/url_test [net/url.test]
//
//	| "net/http" -> net/http [net/url.test]
//	| "net/url" -> net/url [net/url.test]
//	| ...
//
// net/http [net/url.test]
//
//	| "net/url" -> net/url [net/url.test]
//	| ...
//
// This restriction propagates throughout the import graph of net/http: for
// every package imported by net/http that imports net/url, there must be an
// intermediate test variant that instead imports "net/url [net/url.test]".
//
// As one can see from the example of net/url and net/http, intermediate test
// variants can result in many additional packages that are essentially (but
// not quite) identical. For this reason, we filter these variants wherever
// possible.
//
// # Why we mostly ignore intermediate test variants
//
// In projects with complicated tests, there may be a very large
// number of ITVs--asymptotically more than the number of ordinary
// variants. Since they have identical syntax, it is fine in most
// cases to ignore them since the results of analyzing the ordinary
// variant suffice. However, this is not entirely sound.
//
// Consider this package:
//
//	// p/p.go -- in all variants of p
//	package p
//	type T struct { io.Closer }
//
//	// p/p_test.go -- in test variant of p
//	package p
//	func (T) Close() error { ... }
//
// The ordinary variant "p" defines T with a Close method promoted
// from io.Closer. But its test variant "p [p.test]" defines a type T
// with a Close method from p_test.go.
//
// Now consider a package q that imports p, perhaps indirectly. Within
// it, T.Close will resolve to the first Close method:
//
//	// q/q.go -- in all variants of q
//	package q
//	import "p"
//	var _ = new(p.T).Close
//
// Let's assume p also contains this file defining an external test (xtest):
//
//	// p/p_x_test.go -- external test of p
//	package p_test
//	import ( "q"; "testing" )
//	func Test(t *testing.T) { ... }
//
// Note that q imports p, but p's xtest imports q. Now, in "q
// [p.test]", the intermediate test variant of q built for p's
// external test, T.Close resolves not to the io.Closer.Close
// interface method, but to the concrete method of T.Close
// declared in p_test.go.
//
// If we now request all references to the T.Close declaration in
// p_test.go, the result should include the reference from q's ITV.
// (It's not just methods that can be affected; fields can too, though
// it requires bizarre code to achieve.)
//
// As a matter of policy, gopls mostly ignores this subtlety,
// because to account for it would require that we type-check every
// intermediate test variant of p, of which there could be many.
// Good code doesn't rely on such trickery.
//
// Most callers of MetadataForFile call RemoveIntermediateTestVariants
// to discard them before requesting type checking, or the products of
// type-checking such as the cross-reference index or method set index.
//
// MetadataForFile doesn't do this filtering itself becaused in some
// cases we need to make a reverse dependency query on the metadata
// graph, and it's important to include the rdeps of ITVs in that
// query. But the filtering of ITVs should be applied after that step,
// before type checking.
//
// In general, we should never type check an ITV.
func (mp *Package) IsIntermediateTestVariant() bool {
	return mp.ForTest != "" && mp.ForTest != mp.PkgPath && mp.ForTest+"_test" != mp.PkgPath
}

// A Source maps package IDs to metadata for the packages.
//
// TODO(rfindley): replace this with a concrete metadata graph, once it is
// exposed from the snapshot.
type Source interface {
	// Metadata returns the [Package] for the given package ID, or nil if it does
	// not exist.
	// TODO(rfindley): consider returning (*Metadata, bool)
	// TODO(rfindley): consider renaming this method.
	Metadata(PackageID) *Package
}

// TODO(rfindley): move the utility functions below to a util.go file.

// IsCommandLineArguments reports whether a given value denotes
// "command-line-arguments" package, which is a package with an unknown ID
// created by the go command. It can have a test variant, which is why callers
// should not check that a value equals "command-line-arguments" directly.
func IsCommandLineArguments(id PackageID) bool {
	return strings.Contains(string(id), "command-line-arguments")
}

// SortPostOrder sorts the IDs so that if x depends on y, then y appears before x.
func SortPostOrder(meta Source, ids []PackageID) {
	postorder := make(map[PackageID]int)
	order := 0
	var visit func(PackageID)
	visit = func(id PackageID) {
		if _, ok := postorder[id]; !ok {
			postorder[id] = -1 // break recursion
			if mp := meta.Metadata(id); mp != nil {
				for _, depID := range mp.DepsByPkgPath {
					visit(depID)
				}
			}
			order++
			postorder[id] = order
		}
	}
	for _, id := range ids {
		visit(id)
	}
	sort.Slice(ids, func(i, j int) bool {
		return postorder[ids[i]] < postorder[ids[j]]
	})
}

// UnquoteImportPath returns the unquoted import path of s,
// or "" if the path is not properly quoted.
func UnquoteImportPath(spec *ast.ImportSpec) ImportPath {
	path, err := strconv.Unquote(spec.Path.Value)
	if err != nil {
		return ""
	}
	return ImportPath(path)
}

// RemoveIntermediateTestVariants removes intermediate test variants, modifying
// the array. We use a pointer to a slice make it impossible to forget to use
// the result.
func RemoveIntermediateTestVariants(pmetas *[]*Package) {
	metas := *pmetas
	res := metas[:0]
	for _, mp := range metas {
		if !mp.IsIntermediateTestVariant() {
			res = append(res, mp)
		}
	}
	*pmetas = res
}

// IsValidImport returns whether importPkgPath is importable
// by pkgPath.
func IsValidImport(pkgPath, importPkgPath PackagePath) bool {
	i := strings.LastIndex(string(importPkgPath), "/internal/")
	if i == -1 {
		return true
	}
	// TODO(rfindley): this looks wrong: IsCommandLineArguments is meant to
	// operate on package IDs, not package paths.
	if IsCommandLineArguments(PackageID(pkgPath)) {
		return true
	}
	// TODO(rfindley): this is wrong. mod.testx/p should not be able to
	// import mod.test/internal: https://go.dev/play/p/-Ca6P-E4V4q
	return strings.HasPrefix(string(pkgPath), string(importPkgPath[:i]))
}
