// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package cache

import (
	"context"
	"log"
	"maps"
	"slices"
	"strings"

	"golang.org/x/tools/gopls/internal/cache/metadata"
	"golang.org/x/tools/gopls/internal/cache/symbols"
	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/internal/event"
	"golang.org/x/tools/internal/imports"
)

// goplsSource is an imports.Source that provides import information using
// gopls and the module cache index.
type goplsSource struct {
	S         *Snapshot
	envSource *imports.ProcessEnvSource

	// set by each invocation of ResolveReferences
	ctx context.Context
}

func (s *Snapshot) NewGoplsSource(is *imports.ProcessEnvSource) *goplsSource {
	return &goplsSource{
		S:         s,
		envSource: is,
	}
}

func (s *goplsSource) LoadPackageNames(ctx context.Context, srcDir string, paths []imports.ImportPath) (map[imports.ImportPath]imports.PackageName, error) {
	// TODO: use metadata graph. Aside from debugging, this is the only used of envSource
	return s.envSource.LoadPackageNames(ctx, srcDir, paths)
}

type result struct {
	res        *imports.Result
	deprecated bool
}

// ResolveReferences tries to find resolving imports in the workspace, and failing
// that, in the module cache. It uses heuristics to decide among alternatives.
// The heuristics will usually prefer a v2 version, if there is one.
// TODO: It does not take advantage of hints provided by the user:
// 1. syntactic context: pkg.Name().Foo
// 3. already imported files in the same module
func (s *goplsSource) ResolveReferences(ctx context.Context, filename string, missing imports.References) ([]*imports.Result, error) {
	s.ctx = ctx
	// get results from the workspace. There will at most one for each package name
	fromWS, err := s.resolveWorkspaceReferences(filename, missing)
	if err != nil {
		return nil, err
	}
	// collect the ones that are still
	needed := maps.Clone(missing)
	for _, a := range fromWS {
		if _, ok := needed[a.Package.Name]; ok {
			delete(needed, a.Package.Name)
		}
	}
	// when debug (below) is gone, change this to: if len(needed) == 0 {return fromWS, nil}
	var fromCache []*result
	if len(needed) != 0 {
		var err error
		fromCache, err = s.resolveCacheReferences(needed)
		if err != nil {
			return nil, err
		}
		// trim cans to one per missing package.
		byPkgNm := make(map[string][]*result)
		for _, c := range fromCache {
			byPkgNm[c.res.Package.Name] = append(byPkgNm[c.res.Package.Name], c)
		}
		for k, v := range byPkgNm {
			fromWS = append(fromWS, s.bestCache(k, v))
		}
	}
	const debug = false
	if debug { // debugging.
		// what does the old one find?
		old, err := s.envSource.ResolveReferences(ctx, filename, missing)
		if err != nil {
			log.Fatal(err)
		}
		log.Printf("fromCache:%d %s", len(fromCache), filename)
		for i, c := range fromCache {
			log.Printf("cans%d %#v %#v %v", i, c.res.Import, c.res.Package, c.deprecated)
		}
		for k, v := range missing {
			for x := range v {
				log.Printf("missing %s.%s", k, x)
			}
		}
		for k, v := range needed {
			for x := range v {
				log.Printf("needed %s.%s", k, x)
			}
		}

		dbgpr := func(hdr string, v []*imports.Result) {
			for i := 0; i < len(v); i++ {
				log.Printf("%s%d %+v %+v", hdr, i, v[i].Import, v[i].Package)
			}
		}

		dbgpr("fromWS", fromWS)
		dbgpr("old", old)
		s.S.workspacePackages.Range(func(k PackageID, v PackagePath) {
			log.Printf("workspacePackages[%s]=%s", k, v)
		})
		// anything in ans with >1 matches?
		seen := make(map[string]int)
		for _, a := range fromWS {
			seen[a.Package.Name]++
		}
		for k, v := range seen {
			if v > 1 {
				log.Printf("saw %d %s", v, k)
				for i, x := range fromWS {
					if x.Package.Name == k {
						log.Printf("%d: %+v %+v", i, x.Package, x.Import)
					}
				}
			}
		}
	}
	return fromWS, nil

}

func (s *goplsSource) resolveCacheReferences(missing imports.References) ([]*result, error) {
	state := s.S.view.modcacheState
	ix, err := state.GetIndex()
	if err != nil {
		event.Error(s.ctx, "resolveCacheReferences", err)
	}

	found := make(map[string]*result)
	for pkg, nms := range missing {
		var ks []string
		for k := range nms {
			ks = append(ks, k)
		}
		cs := ix.LookupAll(pkg, ks...) // map[importPath][]Candidate
		for k, cands := range cs {
			res := found[k]
			if res == nil {
				res = &result{
					&imports.Result{
						Import:  &imports.ImportInfo{ImportPath: k},
						Package: &imports.PackageInfo{Name: pkg, Exports: make(map[string]bool)},
					},
					false,
				}
				found[k] = res
			}
			for _, c := range cands {
				res.res.Package.Exports[c.Name] = true
				// The import path is deprecated if a symbol that would be used is deprecated
				res.deprecated = res.deprecated || c.Deprecated
			}
		}

	}
	var ans []*result
	for _, x := range found {
		ans = append(ans, x)
	}
	return ans, nil
}

type found struct {
	sym *symbols.Package
	res *imports.Result
}

func (s *goplsSource) resolveWorkspaceReferences(filename string, missing imports.References) ([]*imports.Result, error) {
	uri := protocol.URIFromPath(filename)
	mypkgs, err := s.S.MetadataForFile(s.ctx, uri)
	if len(mypkgs) != 1 {
		// what does this mean? can it happen?
	}
	mypkg := mypkgs[0]
	// search the metadata graph for package ids correstponding to missing
	g := s.S.MetadataGraph()
	var ids []metadata.PackageID
	var pkgs []*metadata.Package
	for pid, pkg := range g.Packages {
		// no test packages, except perhaps for ourselves
		if pkg.ForTest != "" && pkg != mypkg {
			continue
		}
		if missingWants(missing, pkg.Name) {
			ids = append(ids, pid)
			pkgs = append(pkgs, pkg)
		}
	}
	// find the symbols in those packages
	// the syms occur in the same order as the ids and the pkgs
	syms, err := s.S.Symbols(s.ctx, ids...)
	if err != nil {
		return nil, err
	}
	// keep track of used syms and found results by package name
	// TODO: avoid import cycles (is current package in forward closure)
	founds := make(map[string][]found)
	for i := 0; i < len(ids); i++ {
		nm := string(pkgs[i].Name)
		if satisfies(syms[i], missing[nm]) {
			got := &imports.Result{
				Import: &imports.ImportInfo{
					Name:       "",
					ImportPath: string(pkgs[i].PkgPath),
				},
				Package: &imports.PackageInfo{
					Name:    string(pkgs[i].Name),
					Exports: missing[imports.PackageName(pkgs[i].Name)],
				},
			}
			founds[nm] = append(founds[nm], found{syms[i], got})
		}
	}
	var ans []*imports.Result
	for _, v := range founds {
		// make sure the elements of v are unique
		// (Import.ImportPath or Package.Name must differ)
		cmp := func(l, r found) int {
			switch strings.Compare(l.res.Import.ImportPath, r.res.Import.ImportPath) {
			case -1:
				return -1
			case 1:
				return 1
			}
			return strings.Compare(l.res.Package.Name, r.res.Package.Name)
		}
		slices.SortFunc(v, cmp)
		newv := make([]found, 0, len(v))
		newv = append(newv, v[0])
		for i := 1; i < len(v); i++ {
			if cmp(v[i], v[i-1]) != 0 {
				newv = append(newv, v[i])
			}
		}
		ans = append(ans, bestImport(filename, newv))
	}
	return ans, nil
}

// for each package name, choose one using heuristics
func bestImport(filename string, got []found) *imports.Result {
	if len(got) == 1 {
		return got[0].res
	}
	isTestFile := strings.HasSuffix(filename, "_test.go")
	var leftovers []found
	for _, g := range got {
		// don't use _test packages unless isTestFile
		testPkg := strings.HasSuffix(string(g.res.Package.Name), "_test") || strings.HasSuffix(string(g.res.Import.Name), "_test")
		if testPkg && !isTestFile {
			continue // no test covers this
		}
		if imports.CanUse(filename, g.sym.Files[0].DirPath()) {
			leftovers = append(leftovers, g)
		}
	}
	switch len(leftovers) {
	case 0:
		break // use got, they are all bad
	case 1:
		return leftovers[0].res // only one left
	default:
		got = leftovers // filtered some out
	}

	// TODO: if there are versions (like /v2) prefer them

	// use distance to common ancestor with filename
	// (TestDirectoryFilters_MultiRootImportScanning)
	// filename is .../a/main.go, choices are
	// .../a/hi/hi.go and .../b/hi/hi.go
	longest := -1
	ix := -1
	for i := 0; i < len(got); i++ {
		d := commonpref(filename, got[i].sym.Files[0].Path())
		if d > longest {
			longest = d
			ix = i
		}
	}
	// it is possible that there were several tied, but we return the first
	return got[ix].res
}

// choose the best result for the package named nm from the module cache
func (s *goplsSource) bestCache(nm string, got []*result) *imports.Result {
	if len(got) == 1 {
		return got[0].res
	}
	// does the go.mod file choose one?
	if ans := s.fromGoMod(got); ans != nil {
		return ans
	}
	got = preferUndeprecated(got)
	// want the best Import.ImportPath
	// these are all for the package named nm,
	// nm (probably) occurs in all the paths;
	// choose the longest (after nm), so as to get /v2
	maxlen, which := -1, -1
	for i := 0; i < len(got); i++ {
		ix := strings.Index(got[i].res.Import.ImportPath, nm)
		if ix == -1 {
			continue // now what?
		}
		cnt := len(got[i].res.Import.ImportPath) - ix
		if cnt > maxlen {
			maxlen = cnt
			which = i
		}
		// what about ties? (e.g., /v2 and /v3)
	}
	if which >= 0 {
		return got[which].res
	}
	return got[0].res // arbitrary guess
}

// if go.mod requires one of the packages, return that
func (s *goplsSource) fromGoMod(got []*result) *imports.Result {
	// should we use s.S.view.worsspaceModFiles, and the union of their requires?
	// (note that there are no tests where it contains more than one)
	modURI := s.S.view.gomod
	modfh, ok := s.S.files.get(modURI)
	if !ok {
		return nil
	}
	parsed, err := s.S.ParseMod(s.ctx, modfh)
	if err != nil {
		return nil
	}
	reqs := parsed.File.Require
	for _, g := range got {
		for _, req := range reqs {
			if strings.HasPrefix(g.res.Import.ImportPath, req.Syntax.Token[1]) {
				return g.res
			}
		}
	}
	return nil
}

func commonpref(filename string, path string) int {
	k := 0
	for ; k < len(filename) && k < len(path) && filename[k] == path[k]; k++ {
	}
	return k
}

func satisfies(pkg *symbols.Package, missing map[string]bool) bool {
	syms := make(map[string]bool)
	for _, x := range pkg.Symbols {
		for _, s := range x {
			syms[s.Name] = true
		}
	}
	for k := range missing {
		if !syms[k] {
			return false
		}
	}
	return true
}

// does pkgPath potentially satisfy a missing reference?
func missingWants(missing imports.References, pkgPath metadata.PackageName) bool {
	for k := range missing {
		if string(k) == string(pkgPath) {
			return true
		}
	}
	return false
}

// If there are both deprecated and undprecated ones
// then return only the undeprecated one
func preferUndeprecated(got []*result) []*result {
	var ok []*result
	for _, g := range got {
		if !g.deprecated {
			ok = append(ok, g)
		}
	}
	if len(ok) > 0 {
		return ok
	}
	return got
}
