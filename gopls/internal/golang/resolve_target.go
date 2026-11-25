// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package golang

import (
	"cmp"
	"context"
	"fmt"
	"go/build"
	"go/token"
	"go/types"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"golang.org/x/tools/gopls/internal/cache"
	"golang.org/x/tools/gopls/internal/cache/metadata"
	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/gopls/internal/protocol/command"
	"golang.org/x/tools/internal/collection/set"
	"golang.org/x/tools/internal/event"
)

// ResolveTarget resolves a target string and potential package scope to a protocol.Location.
//
// The package is given by the pkgScope, by the target string as
// `<package_path>.<target>`, or by the location of params.TextDocument; see
// [getPackagesAndTarget]. `target` can be a top level symbol in a package or a
// symbol nested within a type, e.g. a field in a struct or a method on an
// interface.
//
// TODO(aputman): Add additional package path resolution logic:
//   - Support standard library packages (e.g. `fmt` in `fmt.Println`, etc.)
//   - (Stretch) If regular resolution doesn't find a package, and the pkgPath that is given
//     is only a pkg name (no directory info given), we could look across the workspace
//     for all pkgs of that name.
//
// TODO(aputman): Support methods on types as nested symbols.
// TODO(aputman): Allow for packages to be the target themselves
// TODO(aputman): Allow for Regex in `ResolveTargetParams.Target`
// TODO(aputman): Apply resolution restriction: `ResolveTargetParams.Range`
// TODO(aputman): Implement block-scope support for sub-targets (e.g. `a.Function.localvar`)
func ResolveTarget(ctx context.Context, snapshot *cache.Snapshot, params command.ResolveTargetParams) (command.ResolveTargetResult, error) {
	ctx, done := event.Start(ctx, "golang.ResolveTarget")
	defer done()

	pkgs, target, err := getPackagesAndTarget(ctx, snapshot, params)
	if err != nil {
		return command.ResolveTargetResult{}, err
	}

	var matches []command.TargetMatch
	var lastErr error
	seen := make(set.Set[protocol.Location]) // dedupe matches
	for _, pkg := range pkgs {
		pkgType := pkg.Types()
		obj, err := resolveTarget(pkgType, target)
		if err != nil {
			lastErr = err
			continue
		}
		loc, err := objLSPLocation(pkg, obj)
		if err != nil {
			lastErr = err
			continue
		}
		if !seen.Insert(loc) {
			continue // Skip duplicate matches (e.g. test variants)
		}
		matches = append(matches, command.TargetMatch{
			Name:     obj.Name(),
			Package:  pkgType.Path(),
			Location: loc,
		})
	}
	if len(matches) == 0 {
		if lastErr != nil {
			// We only return the lastErr after we have exhausted all possible
			// packages to resolve the target in. In that case, any error
			// encountered is potentially the reason no target was found, so we
			// return it.
			return command.ResolveTargetResult{}, lastErr
		}
		return command.ResolveTargetResult{}, fmt.Errorf("target %q not found in %s", target, pkgs[0].Types().Path())
	}

	return command.ResolveTargetResult{
		Matches: matches,
	}, nil
}

// getPackagesAndTarget returns the packages in which to resolve params.Target,
// and the symbol path to resolve within them, using the first rule that applies:
//
//  1. If params.PkgScope is set, it denotes the packages, and the whole target
//     is the symbol path.
//  2. If the target has no slash or dot, it is a symbol in the packages at
//     params.TextDocument; see [packagesAt].
//  3. Otherwise, the target is `<package_path>.<symbol path>`. As a package
//     path may contain dots after its last slash, the longest prefix that
//     denotes a package wins: "example.com/a.b.C" tries "example.com/a.b",
//     then "example.com/a".
//
// See [resolvePkgPath] for the forms a package path may take.
func getPackagesAndTarget(ctx context.Context, snapshot *cache.Snapshot, params command.ResolveTargetParams) ([]*cache.Package, string, error) {
	uri, target := params.TextDocument.URI, params.Target
	if params.PkgScope != "" {
		pkgs, err := resolvePkgPath(ctx, snapshot, uri, params.PkgScope)
		return pkgs, target, err
	}

	// We only allow for target path directories to be specified with "/"s,
	// not necessarily the filepath.Separator for the user's architecture.
	dir, call := path.Split(target)
	parts := strings.Split(call, ".")
	if len(parts) == 1 { // no dot after the last slash
		if dir != "" {
			return nil, "", fmt.Errorf(`target %s is invalid, as it is only a pkg path`, target)
		}
		mps, err := packagesAt(ctx, snapshot, uri)
		if err != nil {
			return nil, "", err
		}
		pkgs, err := typecheckedPkgs(ctx, snapshot, mps)
		return pkgs, target, err
	}

	// TODO(aputman): test prefixes against the standard library packages.
	var lastErr error
	for i := len(parts) - 1; i >= 1; i-- {
		pkgs, err := resolvePkgPath(ctx, snapshot, uri, dir+strings.Join(parts[:i], "."))
		if err == nil {
			return pkgs, strings.Join(parts[i:], "."), nil
		}
		lastErr = err
	}
	if dir == "" {
		// If dir is empty, that means that parts[0] can be a local target query.
		if mps, err := packagesAt(ctx, snapshot, uri); err == nil {
			pkgs, err := typecheckedPkgs(ctx, snapshot, mps)
			return pkgs, target, err
		}
	}
	return nil, "", lastErr // the error for the shortest prefix
}

// resolvePkgPath returns the packages denoted by pkgPath, using the first of
// these rules that matches any package:
//
//  1. A path beginning with "./" or "../" denotes a directory relative to
//     uri. No other rule applies to such paths.
//  2. A package path, such as "example.com/m/a".
//  3. The name of a package in the directory of uri, such as "a" or "a_test".
//  4. A package path relative to the directory of uri, then relative to the
//     root of its module: from directory "a" in module "example.com/m", "b"
//     denotes "example.com/m/a/b" or "example.com/m/b".
//  5. A subdirectory relative to the directory of uri (for package-less
//     directories in GOPATH, GOPACKAGESDRIVER, and go.work roots).
func resolvePkgPath(ctx context.Context, snapshot *cache.Snapshot, uri protocol.DocumentURI, pkgPath string) ([]*cache.Package, error) {
	if filepath.IsAbs(pkgPath) {
		return nil, fmt.Errorf("absolute pkg path not allowed: %s", pkgPath)
	}
	graph, err := snapshot.LoadMetadataGraph(ctx)
	if err != nil {
		return nil, err
	}
	// 1. A directory relative to uri.
	if build.IsLocalImport(pkgPath) {
		if mps := packagesInDir(graph, filepath.Join(uriDir(uri), filepath.FromSlash(pkgPath))); len(mps) > 0 {
			return typecheckedPkgs(ctx, snapshot, mps)
		}
		return nil, fmt.Errorf("package not found: %s", pkgPath)
	}
	// 2. A package path.
	if mps := graph.ForPackagePath[PackagePath(pkgPath)]; len(mps) > 0 {
		return typecheckedPkgs(ctx, snapshot, mps)
	}
	// 3. The name of a package in the directory of uri.
	if mps := packagesNamed(graph, uriDir(uri), PackageName(pkgPath)); len(mps) > 0 {
		return typecheckedPkgs(ctx, snapshot, mps)
	}
	// 4. A package path relative to the directory of uri, then to its module.
	for _, base := range relativeBases(ctx, snapshot, uri) {
		if mps := graph.ForPackagePath[PackagePath(path.Join(base, pkgPath))]; len(mps) > 0 {
			return typecheckedPkgs(ctx, snapshot, mps)
		}
	}
	// 5. Fallback to a subdirectory relative to uri (covers package-less
	// directories in GOPATH, GOPACKAGESDRIVER, and go.work roots).
	if mps := packagesInDir(graph, filepath.Join(uriDir(uri), filepath.FromSlash(pkgPath))); len(mps) > 0 {
		return typecheckedPkgs(ctx, snapshot, mps)
	}
	return nil, fmt.Errorf("package not found: %s", pkgPath)
}

// relativeBases returns the paths against which [resolvePkgPath] resolves a
// relative package path: the path of the package (or directory) at uri, then
// that of its module.
func relativeBases(ctx context.Context, snapshot *cache.Snapshot, uri protocol.DocumentURI) []string {
	// Skip standalone command-line-arguments packages (e.g. //go:build ignore
	// files) so they fall through to the GoModForFile fallback below rather than
	// using "command-line-arguments" as the base.
	if mps, err := packagesAt(ctx, snapshot, uri); err == nil && !metadata.IsCommandLineArguments(mps[0].ID) {
		pkgPath := mps[0].PkgPath
		if mps[0].ForTest != "" {
			// External test files and xtest-only directories have a _test suffix
			// on PkgPath; ForTest holds the directory's base package path.
			pkgPath = mps[0].ForTest
		}
		bases := []string{string(pkgPath)}
		if mps[0].Module != nil {
			bases = append(bases, mps[0].Module.Path)
		}
		return bases
	}
	// Fallback for directories without Go files or standalone ignored files:
	// infer the package path from the enclosing go.mod file.
	modURI := snapshot.GoModForFile(uri)
	if modURI == "" {
		return nil
	}
	fh, err := snapshot.ReadFile(ctx, modURI)
	if err != nil {
		return nil
	}
	pm, err := snapshot.ParseMod(ctx, fh)
	if err != nil || pm.File == nil || pm.File.Module == nil {
		return nil
	}
	modPath := pm.File.Module.Mod.Path
	rel, err := filepath.Rel(modURI.DirPath(), uriDir(uri))
	if err != nil {
		return nil
	}
	return []string{path.Join(modPath, filepath.ToSlash(rel)), modPath}
}

// packagesAt returns the packages at uri: for a file, the widest package
// containing it, which includes its in-package tests; for a directory, all
// packages in it, as reported by [packagesInDir].
func packagesAt(ctx context.Context, snapshot *cache.Snapshot, uri protocol.DocumentURI) ([]*metadata.Package, error) {
	if !isDir(uri) {
		mps, err := snapshot.MetadataForFile(ctx, uri, true)
		if err != nil {
			return nil, err
		}
		if len(mps) == 0 {
			return nil, fmt.Errorf("no package metadata for file %s", uri)
		}
		return mps, nil
	}
	graph, err := snapshot.LoadMetadataGraph(ctx)
	if err != nil {
		return nil, err
	}
	dir := uriDir(uri)
	mps := packagesInDir(graph, dir)
	if len(mps) == 0 {
		return nil, fmt.Errorf("no package metadata for directory %s", dir)
	}
	return mps, nil
}

// packagesInDir returns the packages in dir, other than intermediate test
// variants, sorted by ID, which puts each package before its test packages.
func packagesInDir(graph *metadata.Graph, dir string) []*metadata.Package {
	var mps []*metadata.Package
	for _, mp := range graph.Packages {
		if len(mp.CompiledGoFiles) > 0 && mp.CompiledGoFiles[0].DirPath() == dir && !mp.IsIntermediateTestVariant() {
			mps = append(mps, mp)
		}
	}
	slices.SortFunc(mps, func(x, y *metadata.Package) int { return cmp.Compare(x.ID, y.ID) })
	return mps
}

// packagesNamed returns the packages in dir, as reported by [packagesInDir],
// whose package name is name.
func packagesNamed(graph *metadata.Graph, dir string, name PackageName) []*metadata.Package {
	return slices.DeleteFunc(packagesInDir(graph, dir), func(mp *metadata.Package) bool {
		return mp.Name != name
	})
}

// isDir reports whether uri denotes a directory, that is, has no file extension.
func isDir(uri protocol.DocumentURI) bool {
	return filepath.Ext(uri.Path()) == ""
}

// uriDir returns the directory denoted by uri, or containing the file it denotes.
func uriDir(uri protocol.DocumentURI) string {
	if isDir(uri) {
		return filepath.Clean(uri.Path())
	}
	return uri.DirPath()
}

// resolveTarget looks up a symbol path (without a package prefix) in pkgType.
//
// Only symbols declared at package level in pkgType (pkgType.Scope()) are
// matched; symbols imported by pkgType's files are in each file's scope rather
// than the package scope and are not matched here.
// TODO(golang/go#82877): consider how file-scoped queries in external test
// files (package foo_test) should resolve symbols imported from the package
// under test.
func resolveTarget(pkgType *types.Package, target string) (types.Object, error) {
	scope := pkgType.Scope()
	parts := strings.Split(target, ".")
	var obj types.Object
	var err error
	for i, part := range parts {
		if i == 0 {
			// First, look up the top-level symbol in the package scope.
			obj = scope.Lookup(part)
		} else if obj, err = descend(obj, part); err != nil {
			return nil, err
		}
		if obj == nil {
			return nil, fmt.Errorf(`target %s not found in %s`, part, qualifiedTargetPath(pkgType, parts[:i]))
		}
	}
	return obj, nil
}

func typecheckedPkgs(ctx context.Context, snapshot *cache.Snapshot, mps []*metadata.Package) ([]*cache.Package, error) {
	ids := make([]PackageID, len(mps))
	for i, mp := range mps {
		ids[i] = mp.ID
	}
	return snapshot.TypeCheck(ctx, ids...)
}

func descend(obj types.Object, target string) (types.Object, error) {
	// TODO(aputman): Potentially descend down more types
	switch t := obj.Type().Underlying(); t := t.(type) {
	case *types.Struct:
		for field := range t.Fields() {
			if field.Name() == target {
				return field, nil
			}
		}
		return nil, fmt.Errorf(`field %s not found in %s`, target, obj.Name())
	case *types.Interface:
		for method := range t.Methods() {
			if method.Name() == target {
				return method, nil
			}
		}
		return nil, fmt.Errorf(`method %s not found in %s`, target, obj.Name())
	default:
		return nil, fmt.Errorf(`target resolution not supported for type %T`, t)
	}
}

func qualifiedTargetPath(pkgType *types.Package, parts []string) string {
	return strings.Join(append([]string{pkgType.Path()}, parts...), ".")
}

func objLSPLocation(pkg *cache.Package, obj types.Object) (protocol.Location, error) {
	// Get the parsed go file that contains this object
	pgf, err := pkg.FileEnclosing(obj.Pos())
	if err != nil {
		return protocol.Location{}, err
	}

	// Convert the token.Pos to a protocol.Location.
	// We calculate the end position by adding the length of the object's name.
	return pgf.PosLocation(obj.Pos(), obj.Pos()+token.Pos(len(obj.Name())))
}
