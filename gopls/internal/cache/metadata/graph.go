// Copyright 2022 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package metadata

import (
	"cmp"
	"iter"
	"maps"
	"slices"
	"sort"
	"strings"

	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/gopls/internal/util/bug"
)

// A Graph is an immutable and transitively closed graph of [Package] data.
type Graph struct {
	// Packages maps package IDs to their associated Packages.
	Packages map[PackageID]*Package

	// Each of the three maps below is an index of the pointer values held
	// by the Packages map. However, Package pointers are not generally canonical.

	// ImportedBy maps package IDs to the list of packages that import them.
	ImportedBy map[PackageID][]*Package

	// ForPackagePath maps package by their package path to their package ID.
	// Non-test packages appear before test packages, and within each of those
	// categories, packages with fewer CompiledGoFiles appear first.
	ForPackagePath map[PackagePath][]*Package

	// ForFile maps file URIs to packages, sorted by (!valid, cli, packageID).
	// A single file may belong to multiple packages due to tests packages.
	ForFile map[protocol.DocumentURI][]*Package
}

// Metadata implements the [Source] interface
func (g *Graph) Metadata(id PackageID) *Package {
	return g.Packages[id]
}

// Update creates a new Graph containing the result of applying the given
// updates to the receiver, though the receiver is not itself mutated. As a
// special case, if updates is empty, Update just returns the receiver.
//
// A nil map value is used to indicate a deletion.
func (g *Graph) Update(updates map[PackageID]*Package) *Graph {
	if len(updates) == 0 {
		// Optimization: since the graph is immutable, we can return the receiver.
		return g
	}

	// Debugging golang/go#64227, golang/vscode-go#3126:
	// Assert that the existing metadata graph is acyclic.
	if cycle := cyclic(g.Packages); cycle != "" {
		bug.Reportf("metadata is cyclic even before updates: %s", cycle)
	}
	// Assert that the updates contain no self-cycles.
	for id, mp := range updates {
		if mp != nil {
			for _, depID := range mp.DepsByPkgPath {
				if depID == id {
					bug.Reportf("self-cycle in metadata update: %s", id)
				}
			}
		}
	}

	// Copy pkgs map then apply updates.
	pkgs := make(map[PackageID]*Package, len(g.Packages))
	maps.Copy(pkgs, g.Packages)
	for id, mp := range updates {
		if mp == nil {
			delete(pkgs, id)
		} else {
			pkgs[id] = mp
		}
	}

	// Break import cycles involving updated nodes.
	breakImportCycles(pkgs, updates)

	return newGraph(pkgs)
}

// newGraph returns a new metadataGraph,
// deriving relations from the specified metadata.
func newGraph(pkgs map[PackageID]*Package) *Graph {
	// Build the import graph.
	importedBy := make(map[PackageID][]*Package)
	byPackagePath := make(map[PackagePath][]*Package)
	for _, mp := range pkgs {
		for _, depID := range mp.DepsByPkgPath {
			importedBy[depID] = append(importedBy[depID], mp)
		}
		byPackagePath[mp.PkgPath] = append(byPackagePath[mp.PkgPath], mp)
	}

	// Collect file associations.
	uriPkgs := make(map[protocol.DocumentURI][]*Package)
	for _, mp := range pkgs {
		uris := map[protocol.DocumentURI]struct{}{}
		for _, uri := range mp.CompiledGoFiles {
			uris[uri] = struct{}{}
		}
		for _, uri := range mp.GoFiles {
			uris[uri] = struct{}{}
		}
		for _, uri := range mp.OtherFiles {
			if strings.HasSuffix(string(uri), ".s") { // assembly
				uris[uri] = struct{}{}
			}
		}
		for uri := range uris {
			uriPkgs[uri] = append(uriPkgs[uri], mp)
		}
	}

	// Sort and filter file associations.
	for uri, pkgs := range uriPkgs {
		sort.Slice(pkgs, func(i, j int) bool {
			cli := IsCommandLineArguments(pkgs[i].ID)
			clj := IsCommandLineArguments(pkgs[j].ID)
			if cli != clj {
				return clj
			}

			// 2. packages appear in name order.
			return pkgs[i].ID < pkgs[j].ID
		})

		// Choose the best packages for each URI, according to the following rules:
		//  - If there are any valid real packages, choose them.
		//  - Else, choose the first valid command-line-argument package, if it exists.
		//
		// TODO(rfindley): it might be better to track all packages here, and exclude
		// them later when type checking, but this is the existing behavior.
		for i, pkg := range pkgs {
			// If we've seen *anything* prior to command-line arguments package, take
			// it. Note that pkgs[0] may itself be command-line-arguments.
			if i > 0 && IsCommandLineArguments(pkg.ID) {
				uriPkgs[uri] = pkgs[:i]
				break
			}
		}
	}

	for _, mps := range byPackagePath {
		slices.SortFunc(mps, func(a, b *Package) int {
			if (a.ForTest == "") != (b.ForTest == "") {
				if a.ForTest == "" {
					return -1
				}
				return 1
			}
			if c := cmp.Compare(len(a.CompiledGoFiles), len(b.CompiledGoFiles)); c != 0 {
				return c
			}
			return cmp.Compare(a.ID, b.ID)
		})
	}

	return &Graph{
		Packages:       pkgs,
		ImportedBy:     importedBy,
		ForPackagePath: byPackagePath,
		ForFile:        uriPkgs,
	}
}

// ReverseReflexiveTransitiveClosure returns a new mapping containing the
// metadata for the specified packages along with any package that
// transitively imports one of them, keyed by ID, including all the initial packages.
func (g *Graph) ReverseReflexiveTransitiveClosure(ids ...PackageID) map[PackageID]*Package {
	seen := make(map[PackageID]*Package)
	var visitAll func([]*Package)
	visitAll = func(pkgs []*Package) {
		for _, pkg := range pkgs {
			if seen[pkg.ID] == nil {
				seen[pkg.ID] = pkg
				visitAll(g.ImportedBy[pkg.ID])
			}
		}
	}
	var initial []*Package
	for _, id := range ids {
		if pkg := g.Packages[id]; pkg != nil {
			initial = append(initial, pkg)
		}
	}
	visitAll(initial)
	return seen
}

// ForwardReflexiveTransitiveClosure returns an iterator over the
// specified nodes and all their forward dependencies, in an arbitrary
// topological (dependencies-first) order. The order may vary.
func (g *Graph) ForwardReflexiveTransitiveClosure(ids ...PackageID) iter.Seq[*Package] {
	return func(yield func(*Package) bool) {
		seen := make(map[PackageID]bool)
		var visit func(PackageID) bool
		visit = func(id PackageID) bool {
			if !seen[id] {
				seen[id] = true
				if mp := g.Packages[id]; mp != nil {
					for _, depID := range mp.DepsByPkgPath {
						if !visit(depID) {
							return false
						}
					}
					if !yield(mp) {
						return false
					}
				}
			}
			return true
		}
		for _, id := range ids {
			visit(id)
		}
	}
}

// breakImportCycles breaks import cycles in the metadata by deleting
// Deps* edges. It modifies only metadata present in the 'updates'
// subset. This function has an internal test.
func breakImportCycles(metadata, updates map[PackageID]*Package) {
	// 'go list' should never report a cycle without flagging it
	// as such, but we're extra cautious since we're combining
	// information from multiple runs of 'go list'. Also, Bazel
	// may silently report cycles.
	cycles := detectImportCycles(metadata, updates)
	if len(cycles) > 0 {
		// There were cycles (uncommon). Break them.
		//
		// The naive way to break cycles would be to perform a
		// depth-first traversal and to detect and delete
		// cycle-forming edges as we encounter them.
		// However, we're not allowed to modify the existing
		// Metadata records, so we can only break edges out of
		// the 'updates' subset.
		//
		// Another possibility would be to delete not the
		// cycle forming edge but the topmost edge on the
		// stack whose tail is an updated node.
		// However, this would require that we retroactively
		// undo all the effects of the traversals that
		// occurred since that edge was pushed on the stack.
		//
		// We use a simpler scheme: we compute the set of cycles.
		// All cyclic paths necessarily involve at least one
		// updated node, so it is sufficient to break all
		// edges from each updated node to other members of
		// the strong component.
		//
		// This may result in the deletion of dominating
		// edges, causing some dependencies to appear
		// spuriously unreachable. Consider A <-> B -> C
		// where updates={A,B}. The cycle is {A,B} so the
		// algorithm will break both A->B and B->A, causing
		// A to no longer depend on B or C.
		//
		// But that's ok: any error in Metadata.Errors is
		// conservatively assumed by snapshot.clone to be a
		// potential import cycle error, and causes special
		// invalidation so that if B later drops its
		// cycle-forming import of A, both A and B will be
		// invalidated.
		for _, cycle := range cycles {
			cyclic := make(map[PackageID]bool)
			for _, mp := range cycle {
				cyclic[mp.ID] = true
			}
			for id := range cyclic {
				if mp := updates[id]; mp != nil {
					for path, depID := range mp.DepsByImpPath {
						if cyclic[depID] {
							delete(mp.DepsByImpPath, path)
						}
					}
					for path, depID := range mp.DepsByPkgPath {
						if cyclic[depID] {
							delete(mp.DepsByPkgPath, path)
						}
					}

					// Set m.Errors to enable special
					// invalidation logic in snapshot.clone.
					if len(mp.Errors) == 0 {
						mp.Errors = []packages.Error{{
							Msg:  "detected import cycle",
							Kind: packages.ListError,
						}}
					}
				}
			}
		}

		// double-check when debugging
		if false {
			if cycles := detectImportCycles(metadata, updates); len(cycles) > 0 {
				bug.Reportf("unbroken cycle: %v", cycles)
			}
		}
	}
}

// cyclic returns a description of a cycle,
// if the graph is cyclic, otherwise "".
func cyclic(graph map[PackageID]*Package) string {
	const (
		unvisited = 0
		visited   = 1
		onstack   = 2
	)
	color := make(map[PackageID]int)
	var visit func(id PackageID) string
	visit = func(id PackageID) string {
		switch color[id] {
		case unvisited:
			color[id] = onstack
		case onstack:
			return string(id) // cycle!
		case visited:
			return ""
		}
		if mp := graph[id]; mp != nil {
			for _, depID := range mp.DepsByPkgPath {
				if cycle := visit(depID); cycle != "" {
					return string(id) + "->" + cycle
				}
			}
		}
		color[id] = visited
		return ""
	}
	for id := range graph {
		if cycle := visit(id); cycle != "" {
			return cycle
		}
	}
	return ""
}

// detectImportCycles reports cycles in the metadata graph. It returns a new
// unordered array of all cycles (nontrivial strong components) in the
// metadata graph reachable from a non-nil 'updates' value.
func detectImportCycles(metadata, updates map[PackageID]*Package) [][]*Package {
	// We use the depth-first algorithm of Tarjan.
	// https://doi.org/10.1137/0201010
	//
	// TODO(adonovan): when we can use generics, consider factoring
	// in common with the other implementation of Tarjan (in typerefs),
	// abstracting over the node and edge representation.

	// A node wraps a Metadata with its working state.
	// (Unfortunately we can't intrude on shared Metadata.)
	type node struct {
		rep            *node
		mp             *Package
		index, lowlink int32
		scc            int8 // TODO(adonovan): opt: cram these 1.5 bits into previous word
	}
	nodes := make(map[PackageID]*node, len(metadata))
	nodeOf := func(id PackageID) *node {
		n, ok := nodes[id]
		if !ok {
			mp := metadata[id]
			if mp == nil {
				// Dangling import edge.
				// Not sure whether a go/packages driver ever
				// emits this, but create a dummy node in case.
				// Obviously it won't be part of any cycle.
				mp = &Package{ID: id}
			}
			n = &node{mp: mp}
			n.rep = n
			nodes[id] = n
		}
		return n
	}

	// find returns the canonical node decl.
	// (The nodes form a disjoint set forest.)
	var find func(*node) *node
	find = func(n *node) *node {
		rep := n.rep
		if rep != n {
			rep = find(rep)
			n.rep = rep // simple path compression (no union-by-rank)
		}
		return rep
	}

	// global state
	var (
		index int32 = 1
		stack []*node
		sccs  [][]*Package // set of nontrivial strongly connected components
	)

	// visit implements the depth-first search of Tarjan's SCC algorithm
	// Precondition: x is canonical.
	var visit func(*node)
	visit = func(x *node) {
		x.index = index
		x.lowlink = index
		index++

		stack = append(stack, x) // push
		x.scc = -1

		for _, yid := range x.mp.DepsByPkgPath {
			y := nodeOf(yid)
			// Loop invariant: x is canonical.
			y = find(y)
			if x == y {
				continue // nodes already combined (self-edges are impossible)
			}

			switch {
			case y.scc > 0:
				// y is already a collapsed SCC

			case y.scc < 0:
				// y is on the stack, and thus in the current SCC.
				if y.index < x.lowlink {
					x.lowlink = y.index
				}

			default:
				// y is unvisited; visit it now.
				visit(y)
				// Note: x and y are now non-canonical.
				x = find(x)
				if y.lowlink < x.lowlink {
					x.lowlink = y.lowlink
				}
			}
		}

		// Is x the root of an SCC?
		if x.lowlink == x.index {
			// Gather all metadata in the SCC (if nontrivial).
			var scc []*Package
			for {
				// Pop y from stack.
				i := len(stack) - 1
				y := stack[i]
				stack = stack[:i]
				if x != y || scc != nil {
					scc = append(scc, y.mp)
				}
				if x == y {
					break // complete
				}
				// x becomes y's canonical representative.
				y.rep = x
			}
			if scc != nil {
				sccs = append(sccs, scc)
			}
			x.scc = 1
		}
	}

	// Visit only the updated nodes:
	// the existing metadata graph has no cycles,
	// so any new cycle must involve an updated node.
	for id, mp := range updates {
		if mp != nil {
			if n := nodeOf(id); n.index == 0 { // unvisited
				visit(n)
			}
		}
	}

	return sccs
}
