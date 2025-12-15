// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package completion

// unimported completion is invoked when the user types something like 'foo.xx',
// foo is known to be a package name not yet imported in the current file, and
// xx (or whatever the user has typed) is interpreted as a hint (pattern) for the
// member of foo that the user is looking for.
//
// This code looks for a suitable completion in a number of places. A 'suitable
// completion' is an exported symbol (so a type, const, var, or func) from package
// foo, which, after converting everything to lower case, has the pattern as a
// subsequence.
//
// The code looks for a suitable completion in
// 1. the imports of some other file of the current package,
// 2. the standard library,
// 3. the imports of some other file in the current workspace,
// 4. imports in the current module with 'foo' as the explicit package name,
// 5. the module cache,
// It stops at the first success.

import (
	"context"
	"fmt"
	"go/ast"
	"go/printer"
	"go/token"
	"path"
	"slices"
	"strings"

	"golang.org/x/tools/gopls/internal/cache/metadata"
	"golang.org/x/tools/gopls/internal/golang/completion/snippet"
	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/gopls/internal/util/bug"
	"golang.org/x/tools/internal/imports"
	"golang.org/x/tools/internal/modindex"
	"golang.org/x/tools/internal/stdlib"
	"golang.org/x/tools/internal/versions"
)

func (c *completer) unimported(ctx context.Context, pkgname metadata.PackageName, prefix string) {
	wsIDs, ourIDs := c.findPackageIDs(pkgname)
	stdpkgs := c.stdlibPkgs(pkgname)
	if len(ourIDs) > 0 {
		// use the one in the current package, if possible
		items := c.pkgIDmatches(ctx, ourIDs, pkgname, prefix)
		if c.scoreList(items) {
			return
		}
	}
	// do the stdlib next.
	items := c.stdlibMatches(stdpkgs, pkgname, prefix)
	if c.scoreList(items) {
		return
	}

	// look in the rest of the workspace
	items = c.pkgIDmatches(ctx, wsIDs, pkgname, prefix)
	if c.scoreList(items) {
		return
	}

	// before looking in the module cache, maybe it is an explicit
	// package name used in this module
	if c.explicitPkgName(ctx, pkgname, prefix) {
		return
	}

	// look in the module cache
	items, err := c.modcacheMatches(pkgname, prefix)
	items = c.filterGoMod(ctx, items)
	if err == nil && c.scoreList(items) {
		return
	}

	// out of things to do
}

// prefer completion items that are referenced in the go.mod file
func (c *completer) filterGoMod(ctx context.Context, items []CompletionItem) []CompletionItem {
	if c.pkg.Metadata().Module == nil {
		// for std or GOROOT mode
		return items
	}
	gomod := c.pkg.Metadata().Module.GoMod
	uri := protocol.URIFromPath(gomod)
	fh, err := c.snapshot.ReadFile(ctx, uri)
	if err != nil {
		return items
	}
	pm, err := c.snapshot.ParseMod(ctx, fh)
	if err != nil || pm == nil {
		return items
	}
	// if any of the items match any of the req, just return those
	reqnames := []string{}
	for _, req := range pm.File.Require {
		reqnames = append(reqnames, req.Mod.Path)
	}
	better := []CompletionItem{}
	for _, compl := range items {
		if len(compl.AdditionalTextEdits) == 0 {
			continue
		}
		// import "foof/pkg"
		flds := strings.FieldsFunc(compl.AdditionalTextEdits[0].NewText, func(r rune) bool {
			return r == '"' || r == '/'
		})
		if len(flds) < 3 {
			continue
		}
		if slices.Contains(reqnames, flds[1]) {
			better = append(better, compl)
		}
	}
	if len(better) > 0 {
		return better
	}
	return items
}

// see if some file in the current package satisfied a foo. import
// because foo is an explicit package name (import foo "a.b.c")
func (c *completer) explicitPkgName(ctx context.Context, pkgname metadata.PackageName, prefix string) bool {
	for _, pgf := range c.pkg.CompiledGoFiles() {
		imports := pgf.File.Imports
		for _, imp := range imports {
			if imp.Name != nil && imp.Name.Name == string(pkgname) {
				path := strings.Trim(imp.Path.Value, `"`)
				if c.tryPath(ctx, metadata.PackagePath(path), string(pkgname), prefix) {
					return true // one is enough
				}
			}
		}
	}
	return false
}

// see if this path contains a usable import with explict package name
func (c *completer) tryPath(ctx context.Context, path metadata.PackagePath, pkgname, prefix string) bool {
	packages := c.snapshot.MetadataGraph().ForPackagePath
	ids := []metadata.PackageID{}
	for _, pkg := range packages[path] { // could there ever be more than one?
		ids = append(ids, pkg.ID) // pkg.ID. ID: "math/rand" but Name: "rand"
	}
	items := c.pkgIDmatches(ctx, ids, metadata.PackageName(pkgname), prefix)
	return c.scoreList(items)
}

// find all the packageIDs for packages in the workspace that have the desired name
// thisPkgIDs contains the ones known to the current package, wsIDs contains the others
func (c *completer) findPackageIDs(pkgname metadata.PackageName) (wsIDs, thisPkgIDs []metadata.PackageID) {
	g := c.snapshot.MetadataGraph()
	for pid, pkg := range c.snapshot.MetadataGraph().Packages {
		if pkg.Name != pkgname {
			continue
		}
		imports := g.ImportedBy[pid]
		// Metadata is not canonical: it may be held onto by a package. Therefore,
		// we must compare by ID.
		thisPkg := func(mp *metadata.Package) bool { return mp.ID == c.pkg.Metadata().ID }
		if slices.ContainsFunc(imports, thisPkg) {
			thisPkgIDs = append(thisPkgIDs, pid)
		} else {
			wsIDs = append(wsIDs, pid)
		}
	}
	return
}

// find all the stdlib packages that have the desired name
func (c *completer) stdlibPkgs(pkgname metadata.PackageName) []metadata.PackagePath {
	var pkgs []metadata.PackagePath // stlib packages that match pkg
	for pkgpath := range stdlib.PackageSymbols {
		v := metadata.PackageName(path.Base(pkgpath))
		if v == pkgname {
			pkgs = append(pkgs, metadata.PackagePath(pkgpath))
		} else if imports.WithoutVersion(string(pkgpath)) == string(pkgname) {
			pkgs = append(pkgs, metadata.PackagePath(pkgpath))
		}
	}
	return pkgs
}

// return CompletionItems for all matching symbols in the packages in ids.
func (c *completer) pkgIDmatches(ctx context.Context, ids []metadata.PackageID, pkgname metadata.PackageName, prefix string) []CompletionItem {
	pattern := strings.ToLower(prefix)
	allpkgsyms, err := c.snapshot.Symbols(ctx, ids...)
	if err != nil {
		return nil // would if be worth retrying the ids one by one?
	}
	if len(allpkgsyms) != len(ids) {
		bug.Reportf("Symbols returned %d values for %d pkgIDs", len(allpkgsyms), len(ids))
		return nil
	}
	var got []CompletionItem
	for i, pkgID := range ids {
		pkg := c.snapshot.MetadataGraph().Packages[pkgID]
		if pkg == nil {
			bug.Reportf("no metadata for %s", pkgID)
			continue // something changed underfoot, otherwise can't happen
		}
		pkgsyms := allpkgsyms[i]
		pkgfname := pkgsyms.Files[0].Path()
		if !imports.CanUse(c.filename, pkgfname) {
			// avoid unusable internal, etc
			continue
		}
		// are any of these any good?
		for np, asym := range pkgsyms.Symbols {
			for _, sym := range asym {
				if !token.IsExported(sym.Name) {
					continue
				}
				if !usefulCompletion(sym.Name, pattern) {
					// for json.U, the existing code finds InvalidUTF8Error
					continue
				}
				var params []string
				var kind protocol.CompletionItemKind
				var detail string
				switch sym.Kind {
				case protocol.Function:
					foundURI := pkgsyms.Files[np]
					fh, _ := c.snapshot.ReadFile(ctx, foundURI)
					pgf, err := c.snapshot.ParseGo(ctx, fh, 0)
					if err == nil {
						params = funcParams(pgf.File, sym.Name)
					}
					kind = protocol.FunctionCompletion
					detail = fmt.Sprintf("func (from %q)", pkg.PkgPath)
				case protocol.Variable, protocol.Struct:
					kind = protocol.VariableCompletion
					detail = fmt.Sprintf("var (from %q)", pkg.PkgPath)
				case protocol.Constant:
					kind = protocol.ConstantCompletion
					detail = fmt.Sprintf("const (from %q)", pkg.PkgPath)
				default:
					continue
				}
				got = c.appendNewItem(got, sym.Name,
					detail,
					pkg.PkgPath,
					kind,
					pkgname, params)
			}
		}
	}
	return got
}

// return CompletionItems for all the matches in packages in pkgs.
func (c *completer) stdlibMatches(pkgs []metadata.PackagePath, pkg metadata.PackageName, prefix string) []CompletionItem {
	// check for deprecated symbols someday
	got := make([]CompletionItem, 0)
	pattern := strings.ToLower(prefix)
	// avoid non-determinacy, especially for marker tests
	slices.Sort(pkgs)
	for _, candpkg := range pkgs {
		if std, ok := stdlib.PackageSymbols[string(candpkg)]; ok {
			for _, sym := range std {
				if !usefulCompletion(sym.Name, pattern) {
					continue
				}
				if !versions.AtLeast(c.goversion, sym.Version.String()) {
					continue
				}
				var kind protocol.CompletionItemKind
				var detail string
				var params []string
				switch sym.Kind {
				case stdlib.Func:
					params = parseSignature(sym.Signature)
					kind = protocol.FunctionCompletion
					detail = fmt.Sprintf("func (from %q)", candpkg)
				case stdlib.Const:
					kind = protocol.ConstantCompletion
					detail = fmt.Sprintf("const (from %q)", candpkg)
				case stdlib.Var:
					kind = protocol.VariableCompletion
					detail = fmt.Sprintf("var (from %q)", candpkg)
				case stdlib.Type:
					kind = protocol.VariableCompletion
					detail = fmt.Sprintf("type (from %q)", candpkg)
				default:
					continue
				}
				got = c.appendNewItem(got, sym.Name,
					detail,
					candpkg,
					kind,
					pkg, params)
			}
		}
	}
	return got
}

func (c *completer) modcacheMatches(pkg metadata.PackageName, prefix string) ([]CompletionItem, error) {
	ix, err := c.snapshot.View().ModcacheIndex()
	if err != nil {
		return nil, err
	}
	// retrieve everything and let usefulCompletion() and the matcher sort them out
	cands := ix.Lookup(string(pkg), "", true)
	lx := len(cands)
	got := make([]CompletionItem, 0, lx)
	pattern := strings.ToLower(prefix)
	for _, cand := range cands {
		if !usefulCompletion(cand.Name, pattern) {
			continue
		}
		var params []string
		var kind protocol.CompletionItemKind
		var detail string
		switch cand.Type {
		case modindex.Func:
			for _, f := range cand.Sig {
				params = append(params, fmt.Sprintf("%s %s", f.Arg, f.Type))
			}
			kind = protocol.FunctionCompletion
			detail = fmt.Sprintf("func (from %s)", cand.ImportPath)
		case modindex.Var:
			kind = protocol.VariableCompletion
			detail = fmt.Sprintf("var (from %s)", cand.ImportPath)
		case modindex.Const:
			kind = protocol.ConstantCompletion
			detail = fmt.Sprintf("const (from %s)", cand.ImportPath)
		case modindex.Type: // might be a type alias
			kind = protocol.VariableCompletion
			detail = fmt.Sprintf("type (from %s)", cand.ImportPath)
		default:
			continue
		}
		got = c.appendNewItem(got, cand.Name,
			detail,
			metadata.PackagePath(cand.ImportPath),
			kind,
			pkg, params)
	}
	return got, nil
}

func (c *completer) appendNewItem(got []CompletionItem, name, detail string, path metadata.PackagePath, kind protocol.CompletionItemKind, pkg metadata.PackageName, params []string) []CompletionItem {
	item := CompletionItem{
		Label:      name,
		Detail:     detail,
		InsertText: name,
		Kind:       kind,
	}
	imp := importInfo{
		importPath: string(path),
		name:       string(pkg),
	}
	if imports.ImportPathToAssumedName(string(path)) == string(pkg) {
		imp.name = ""
	}
	item.AdditionalTextEdits, _ = c.importEdits(&imp)
	if params != nil {
		var sn snippet.Builder
		c.functionCallSnippet(name, nil, params, &sn)
		item.snippet = &sn
	}
	got = append(got, item)
	return got
}

// score the list. Return true if any item is added to c.items
func (c *completer) scoreList(items []CompletionItem) bool {
	ret := false
	for _, item := range items {
		item.Score = float64(c.matcher.Score(item.Label))
		if item.Score > 0 {
			c.items = append(c.items, item)
			ret = true
		}
	}
	return ret
}

// pattern is always the result of strings.ToLower
func usefulCompletion(name, pattern string) bool {
	// this travesty comes from foo.(type) somehow. see issue59096.txt
	if pattern == "_" {
		return true
	}
	// convert both to lower case, and then the runes in the pattern have to occur, in order,
	// in the name
	cand := strings.ToLower(name)
	for _, r := range pattern {
		ix := strings.IndexRune(cand, r)
		if ix < 0 {
			return false
		}
		cand = cand[ix+1:]
	}
	return true
}

// return a printed version of the function arguments for snippets
func funcParams(f *ast.File, fname string) []string {
	var params []string
	setParams := func(list *ast.FieldList) {
		if list == nil {
			return
		}
		var cfg printer.Config // slight overkill
		param := func(name string, typ ast.Expr) {
			var buf strings.Builder
			buf.WriteString(name)
			buf.WriteByte(' ')
			cfg.Fprint(&buf, token.NewFileSet(), typ) // ignore error
			params = append(params, buf.String())
		}

		for _, field := range list.List {
			if field.Names != nil {
				for _, name := range field.Names {
					param(name.Name, field.Type)
				}
			} else {
				param("_", field.Type)
			}
		}
	}
	for _, n := range f.Decls {
		switch x := n.(type) {
		case *ast.FuncDecl:
			if x.Recv == nil && x.Name.Name == fname {
				setParams(x.Type.Params)
			}
		}
	}
	return params
}

// extract the formal parameters from the signature.
// func[M1 ~map[K]V, M2 ~map[K]V, K comparable, V any](dst M1, src M2) -> []{"dst M1", "src M2"}
// func[K comparable, V any](seq iter.Seq2[K, V]) map[K]V -> []{"seq iter.Seq2[K, V]"}
// func(args ...any) *Logger -> []{"args ...any"}
// func[M ~map[K]V, K comparable, V any](m M, del func(K, V) bool) -> []{"m M", "del func(K, V) bool"}
func parseSignature(sig string) []string {
	var level int       // nesting level of delimiters
	var processing bool // are we doing the params
	var last int        // start of current parameter
	var params []string
	for i := range len(sig) {
		switch sig[i] {
		case '[', '{':
			level++
		case ']', '}':
			level--
		case '(':
			level++
			if level == 1 {
				processing = true
				last = i + 1
			}
		case ')':
			level--
			if level == 0 && processing { // done
				if i > last {
					params = append(params, strings.TrimSpace(sig[last:i]))
				}
				return params
			}
		case ',':
			if level == 1 && processing {
				params = append(params, strings.TrimSpace(sig[last:i]))
				last = i + 1
			}
		}
	}
	return nil
}
