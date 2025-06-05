// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package splitpkg

// This file produces the "Split package" HTML report.
//
// The server persistently holds, for each PackageID, the current set
// of components and the mapping from declared names to components. On
// each page reload or JS reload() call, the server type-checks the
// package, computes its symbol reference graph, projects it onto
// components, then returns the component reference graph, and if it
// is cyclic, which edges form cycles. Thus changes to the package
// source are reflected in the client UI at the next page reload or JS
// reload() event.
//
// See also:
// - ../codeaction.go - offers the CodeAction command
// - ../../server/command.go - handles the command by opening a web page
// - ../../server/server.go - handles the HTTP request and calls this function
// - ../../server/assets/splitpkg.js - client-side logic
// - ../../test/integration/web/splitpkg_test.go - integration test of server
//
// TODO(adonovan): future work
//
// Refine symbol reference graph:
// - deal with enums (values must stay together; implicit dependency on iota expression)
// - deal with coupled vars "var x, y = f()"
// - deal with declared methods (coupled to receiver named type)
// - deal with fields/interface methods (loosely coupled to struct/interface type)
//   In both cases the field/method name must be either exported or in the same component.
//
// UI:
// - make shift click extend selection of a range of checkboxes.
// - display two-level grouping of decls and specs: var ( x int; y int )
// - indicate when package has type errors (data may be incomplete).
//
// Code transformation:
// - add "Split" button that is green when acyclic. It should:
//   1) move each component into a new package, or separate file of
//      the same package. (The UI will need to hold this user
//      intent in the list of components.)
//   2) ensure that each declaration referenced from another package
//      is public, renaming as needed.
//   3) update package decls, imports, package docs, file docs,
//      doc comments, etc.
// Should we call this feature "Reorganize package" or "Decompose package"
// until the "Split" button actually exists?

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"html/template"
	"log"
	"strconv"
	"strings"

	"golang.org/x/tools/gopls/internal/cache"
	"golang.org/x/tools/gopls/internal/cache/metadata"
	"golang.org/x/tools/gopls/internal/filecache"
	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/gopls/internal/util/moremaps"
	"golang.org/x/tools/gopls/internal/util/safetoken"
	"golang.org/x/tools/internal/typesinternal"
)

//go:embed splitpkg.html.tmpl
var htmlTmpl string

// HTML returns the HTML for the main "Split package" page, for the
// /splitpkg endpoint. The real magic happens in JavaScript; see
// ../../server/assets/splitpkg.js.
func HTML(pkgpath metadata.PackagePath) []byte {
	t, err := template.New("splitpkg.html").Parse(htmlTmpl)
	if err != nil {
		log.Fatal(err)
	}
	data := struct {
		Title string
	}{
		Title: fmt.Sprintf("Split package %s", pkgpath),
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		log.Fatal(err)
	}
	return buf.Bytes()
}

const cacheKind = "splitpkg" // filecache kind

func cacheKey(pkgID metadata.PackageID) [32]byte {
	return sha256.Sum256([]byte(pkgID))
}

// UpdateComponentsJSON parses the JSON description of components and
// their assigned declarations and updates the component state for the
// specified package.
func UpdateComponentsJSON(pkgID metadata.PackageID, data []byte) error {
	return filecache.Set(cacheKind, cacheKey(pkgID), data)
}

// Web is an abstraction of gopls' web server.
type Web interface {
	// SrcURL forms URLs that cause the editor to open a file at a specific position.
	SrcURL(filename string, line, col8 int) protocol.URI
}

// JSON returns the JSON encoding of the data needed by
// the /splitpkg-json endpoint for the specified package. It includes:
//   - the set of names declared by the package, grouped by file;
//   - the set of components and their assigned declarations from
//     the most recent call to [UpdateComponentsJSON]; and
//   - the component graph derived from them, along with the
//     sets of reference that give rise to each edge.
func JSON(pkg *cache.Package, web Web) ([]byte, error) {
	// Retrieve package's most recent state from the file cache.
	var comp ComponentsJSON
	data, err := filecache.Get(cacheKind, cacheKey(pkg.Metadata().ID))
	if err != nil {
		if err != filecache.ErrNotFound {
			return nil, err
		}
		// cache miss: use zero value
	} else if err := json.Unmarshal(data, &comp); err != nil {
		return nil, err
	}

	// Prepare to construct symbol reference graph.
	var (
		info    = pkg.TypesInfo()
		symbols = make(map[types.Object]*symbol)
	)

	// setName records the UI name for an object.
	// (The UI name disambiguates "init", "_", etc.)
	setName := func(obj types.Object, name string) {
		symbols[obj] = &symbol{
			name:      name,
			component: comp.Assignments[name], // missing => "default"
		}
	}

	// Pass 1: name everything, since naming is order-dependent.
	var initCounter, blankCounter int
	for _, pgf := range pkg.CompiledGoFiles() {
		for _, decl := range pgf.File.Decls {
			switch decl := decl.(type) {
			case *ast.FuncDecl:
				if fn, ok := info.Defs[decl.Name].(*types.Func); ok {
					// For now we treat methods as first class decls,
					// but since they are coupled to the named type
					// they should be omitted in the UI for brevity.
					name := fn.Name()
					if recv := fn.Signature().Recv(); recv != nil {
						fn = fn.Origin()
						_, named := typesinternal.ReceiverNamed(recv)
						name = named.Obj().Name() + "." + name
					} else if name == "init" {
						// Disambiguate top-level init functions.
						name += suffix(&initCounter)
					}
					if name == "_" { // (function or method)
						name += suffix(&blankCounter)
					}
					setName(fn, name)
				}

			case *ast.GenDecl:
				switch decl.Tok {
				case token.CONST, token.VAR:
					for _, spec := range decl.Specs {
						spec := spec.(*ast.ValueSpec)
						for _, id := range spec.Names {
							if obj := info.Defs[id]; obj != nil {
								name := obj.Name()
								if name == "_" {
									name += suffix(&blankCounter)
								}
								setName(obj, name)
							}
						}
					}

				case token.TYPE:
					for _, spec := range decl.Specs {
						spec := spec.(*ast.TypeSpec)
						if obj := info.Defs[spec.Name]; obj != nil {
							name := obj.Name()
							if name == "_" {
								name += suffix(&blankCounter)
							}
							setName(obj, name)
						}
					}
				}
			}
		}
	}

	// Pass 2: compute symbol reference graph, project onto
	// component dependency graph, and build JSON response.
	var (
		files []*fileJSON
		refs  []*refJSON
	)
	for _, pgf := range pkg.CompiledGoFiles() {
		identURL := func(id *ast.Ident) string {
			posn := safetoken.Position(pgf.Tok, id.Pos())
			return web.SrcURL(posn.Filename, posn.Line, posn.Column)
		}
		newCollector := func(from *symbol) *refCollector {
			return &refCollector{
				from:     from,
				identURL: identURL,
				pkg:      pkg.Types(),
				info:     info,
				symbols:  symbols,
			}
		}
		var decls []*declJSON
		for _, decl := range pgf.File.Decls {
			var (
				kind  string
				specs []*specJSON
			)
			switch decl := decl.(type) {
			case *ast.FuncDecl:
				kind = "func"
				if fn, ok := info.Defs[decl.Name].(*types.Func); ok {
					symbol := symbols[fn]
					rc := newCollector(symbol).collect(decl)
					refs = append(refs, rc.refs...)
					specs = append(specs, &specJSON{
						Name: symbol.name,
						URL:  identURL(decl.Name),
					})
				}

			case *ast.GenDecl:
				kind = decl.Tok.String()

				switch decl.Tok {
				case token.CONST, token.VAR:
					for _, spec := range decl.Specs {
						spec := spec.(*ast.ValueSpec)
						for i, id := range spec.Names {
							if obj := info.Defs[id]; obj != nil {
								symbol := symbols[obj]
								rc := newCollector(symbol)
								// If there's a type,
								// all RHSs depend on it.
								if spec.Type != nil {
									rc.collect(spec.Type)
								}
								switch len(spec.Values) {
								case len(spec.Names):
									// var x, y = a, b
									rc.collect(spec.Values[i])
								case 1:
									// var x, y = f()
									rc.collect(spec.Values[0])
								case 0:
									// var x T
								}
								refs = append(refs, rc.refs...)
								specs = append(specs, &specJSON{
									Name: symbol.name,
									URL:  identURL(id),
								})
							}
						}
					}

				case token.TYPE:
					for _, spec := range decl.Specs {
						spec := spec.(*ast.TypeSpec)
						if obj := info.Defs[spec.Name]; obj != nil {
							symbol := symbols[obj]
							rc := newCollector(symbol).collect(spec.Type)
							refs = append(refs, rc.refs...)
							specs = append(specs, &specJSON{
								Name: symbol.name,
								URL:  identURL(spec.Name),
							})
						}
					}
				}
			}
			if len(specs) > 0 {
				decls = append(decls, &declJSON{Kind: kind, Specs: specs})
			}
		}
		files = append(files, &fileJSON{
			Base:  pgf.URI.Base(),
			URL:   web.SrcURL(pgf.URI.Path(), 1, 1),
			Decls: decls,
		})
	}

	// Compute the graph of dependencies between components, by
	// projecting the symbol dependency graph through component
	// assignments.
	var (
		g        = make(graph)
		edgeRefs = make(map[[2]int][]*refJSON) // refs that induce each intercomponent edge
	)
	for _, ref := range refs {
		from, to := ref.from, ref.to
		if from.component != to.component {
			// inter-component reference
			m, ok := g[from.component]
			if !ok {
				m = make(map[int]bool)
				g[from.component] = m
			}
			m[to.component] = true

			key := [2]int{from.component, to.component}
			edgeRefs[key] = append(edgeRefs[key], ref)
		}
	}

	// Detect cycles in the component graph
	// and record cyclic (⚠) components.
	cycles := [][]int{}        // non-nil for JSON
	scmap := make(map[int]int) // maps component index to 1 + SCC index (0 => acyclic)
	for i, scc := range sccs(g) {
		for c := range scc {
			scmap[c] = i + 1
		}
		cycles = append(cycles, moremaps.KeySlice(scc))
	}

	// Record intercomponent edges and their references.
	edges := []*edgeJSON{} // non-nil for JSON
	for edge, refs := range edgeRefs {
		from, to := edge[0], edge[1]
		edges = append(edges, &edgeJSON{
			From:   from,
			To:     to,
			Refs:   refs,
			Cyclic: scmap[from] > 0 && scmap[from] == scmap[to],
		})
	}

	return json.Marshal(ResultJSON{
		Files:      files,
		Components: comp,
		Edges:      edges,
		Cycles:     cycles,
	})
}

// A refCollector gathers intra-package references to top-level
// symbols from within one syntax tree, in lexical order.
type refCollector struct {
	from     *symbol
	identURL func(*ast.Ident) string
	pkg      *types.Package
	info     *types.Info
	index    map[types.Object]*refJSON
	symbols  map[types.Object]*symbol

	refs []*refJSON // output
}

// A symbol describes a declared name and its assigned component.
type symbol struct {
	name      string // unique name in the UI and JSON/HTTP protocol
	component int    // index of assigned component
}

// collect adds the free references of n to the collection.
func (rc *refCollector) collect(n ast.Node) *refCollector {
	var f func(n ast.Node) bool
	f = func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.SelectorExpr:
			if sel, ok := rc.info.Selections[n]; ok {
				rc.addRef(n.Sel, sel.Obj())
				ast.Inspect(n.X, f)
				return false // don't visit n.Sel
			}

		case *ast.Ident:
			if obj := rc.info.Uses[n]; obj != nil {
				rc.addRef(n, obj)
			}
		}
		return true
	}
	ast.Inspect(n, f)

	return rc
}

// addRef records a reference from id to obj.
func (rc *refCollector) addRef(id *ast.Ident, obj types.Object) {
	if obj.Pkg() != rc.pkg {
		return // cross-package reference
	}

	// Un-instantiate methods.
	if fn, ok := obj.(*types.Func); ok && fn.Signature().Recv() != nil {
		obj = fn.Origin() // G[int].method -> G[T].method
	}

	// We only care about refs to package-level symbols.
	// And methods, for now.
	decl := rc.symbols[obj]
	if decl == nil {
		return // not a package-level symbol or top-level method
	}

	if ref, ok := rc.index[obj]; !ok {
		ref = &refJSON{
			From: rc.from.name,
			To:   decl.name,
			URL:  rc.identURL(id),
			from: rc.from,
			to:   decl,
		}
		if rc.index == nil {
			rc.index = make(map[types.Object]*refJSON)
		}
		rc.index[obj] = ref
		rc.refs = append(rc.refs, ref)
	}
}

// suffix returns a subscripted decimal suffix,
// preincrementing the specified counter.
func suffix(counter *int) string {
	*counter++
	n := *counter
	return subscripter.Replace(strconv.Itoa(n))
}

var subscripter = strings.NewReplacer(
	"0", "₀",
	"1", "₁",
	"2", "₂",
	"3", "₃",
	"4", "₄",
	"5", "₅",
	"6", "₆",
	"7", "₇",
	"8", "₈",
	"9", "₉",
)

// -- JSON types --

// ResultJSON describes the result of a /splitpkg-json query.
// It is public for testing.
type ResultJSON struct {
	Components ComponentsJSON // component names and their assigned declarations
	Files      []*fileJSON    // files of the packages and their declarations and references
	Edges      []*edgeJSON    // inter-component edges and their references
	Cycles     [][]int        // sets of strongly-connected components
}

// request body of a /splitpkg-components update;
// also part of /splitpkg-json response.
type ComponentsJSON struct {
	Names       []string       `json:",omitempty"` // if empty, implied Names[0]=="default".
	Assignments map[string]int `json:",omitempty"` // maps specJSON.Name to component index; missing => 0
}

// edgeJSON describes an inter-component dependency.
type edgeJSON struct {
	From, To int        // component IDs
	Refs     []*refJSON // references that give rise to this edge
	Cyclic   bool       // edge is part of nontrivial strongly connected component
}

// fileJSON records groups decl/spec information about a single file.
type fileJSON struct {
	Base  string      // file base name
	URL   string      // showDocument link for file
	Decls []*declJSON `json:",omitempty"`
}

// declJSON groups specs (e.g. "var ( x int; y int )").
type declJSON struct {
	Kind  string      // const, var, type, func
	Specs []*specJSON `json:",omitempty"`
}

// specJSON describes a single declared name.
// (A coupled declaration "var x, y = f()" results in two specJSONs.)
type specJSON struct {
	Name string // x or T.x
	URL  string // showDocument link for declaring identifier
}

// refJSON records the first reference from a given declaration to a symbol.
// (Repeat uses of the same identifier are omitted.)
type refJSON struct {
	From, To string // x or T.x of referenced spec
	URL      string // showDocument link for referring identifier

	from, to *symbol // transient
}
