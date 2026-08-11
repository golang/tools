// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package golang

import (
	"context"
	"fmt"
	"go/token"
	"go/types"
	"path"
	"path/filepath"
	"strings"

	"golang.org/x/tools/gopls/internal/cache"
	"golang.org/x/tools/gopls/internal/cache/metadata"
	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/gopls/internal/protocol/command"
	"golang.org/x/tools/internal/event"
)

// ResolveTarget resolves a target string and potential package scope to a protocol.Location.
//
// This currently only handles fully qualified package paths, either in the
// pkgScope or in the target string as `<package_path>.<target>`. `target` can
// be a top level symbol in a package or a symbol nested within a type, e.g.
// a field in a struct or a method on an interface.
//
// TODO(aputman): Add additional package path resolution logic:
//   - Support standard library packages (e.g. `fmt` in `fmt.Println`, etc.)
//   - CWD Mod file relative package paths (e.g. `b.pkgA`)
//   - (maybe) local relative package paths (e.g. `../b/pkgA`)
//
// TODO(aputman): Allow for Regex in `ResolveTargbetParams.Target`
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
	seen := make(map[protocol.Location]bool) // dedupe matches
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
		if seen[loc] {
			continue // Skip duplicate matches (e.g. test variants)
		}
		seen[loc] = true
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

func getPackagesAndTarget(ctx context.Context, snapshot *cache.Snapshot, params command.ResolveTargetParams) ([]*cache.Package, string, error) {
	target := params.Target

	// There is a deterministic order to resolving a package path. Follow the numbered comments below.

	if params.PkgScope != "" {
		// (1) If passed an explicit pkgScope, the target is assumed to have no package info.
		pkgs, err := resolvePkgStr(ctx, snapshot, params.PkgScope)
		return pkgs, target, err
	}

	dir, call := path.Split(target)

	// TODO(aputman): Add CWD local package path resolution here.

	// (2) Lexical fallback: split at the first dot after the last slash
	pkgPart, targetPart, found := strings.Cut(call, ".")
	if !found || targetPart == "" {
		return nil, "", fmt.Errorf(`target %s is invalid, as it is only a pkg path`, target)
	}

	fallbackPkgPath := path.Join(dir, pkgPart)
	pkgs, err := resolvePkgStr(ctx, snapshot, fallbackPkgPath)
	return pkgs, targetPart, err
}

func resolvePkgStr(ctx context.Context, snapshot *cache.Snapshot, pkgStr string) ([]*cache.Package, error) {
	if filepath.IsAbs(pkgStr) {
		return nil, fmt.Errorf("absolute pkg path not allowed: %s", pkgStr)
	}
	graph, err := snapshot.LoadMetadataGraph(ctx)
	if err != nil {
		return nil, err
	}

	metas := graph.ForPackagePath[metadata.PackagePath(pkgStr)]

	// TODO(aputman): Add logic for relative package paths.

	pkgs, err := loadPackagesFromMetas(ctx, snapshot, metas)
	if err != nil {
		return nil, err
	}
	if len(pkgs) == 0 {
		return nil, fmt.Errorf("package not found: %s", pkgStr)
	}
	return pkgs, nil
}

func loadPackagesFromMetas(ctx context.Context, snapshot *cache.Snapshot, metas []*metadata.Package) ([]*cache.Package, error) {
	var pkgs []*cache.Package
	for _, m := range metas {
		tmpPkgs, err := snapshot.TypeCheck(ctx, m.ID)
		if err != nil || len(tmpPkgs) == 0 {
			return nil, err
		}
		pkgs = append(pkgs, tmpPkgs...)
	}
	return pkgs, nil
}

// At this point, the target should no longer have any pkg information.
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
