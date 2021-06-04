// Copyright 2017 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package render

import (
	"fmt"
	"go/ast"
	"go/doc"
	"go/token"
	"html/template"
	"strings"
	"unicode"
	"unicode/utf8"
)

/*
This logic creates data structures needed to map some string (e.g., "io.EOF")
to the package that contains those identifiers.

The identifierResolver.toHTML method is the primary API used by other logic
in this package. The identifierResolver is essentially comprised of:

	* packageIDs: a collection of top-level identifiers in the package being
	  rendered and any related packages.

	* declIDs: a collection of parameters related to the specific declaration
	  being rendered. We collect declaration parameters because they provide
	  a useful heuristic for further linking (e.g., "r.Read" can be linked
	  if we know the type of r).

This logic is unused when doing non-HTML formatting.
*/

// forEachPackageDecl iterates though every top-level declaration in a package.
func forEachPackageDecl(pkg *doc.Package, fnc func(decl ast.Decl)) {
	for _, c := range pkg.Consts {
		fnc(c.Decl)
	}
	for _, v := range pkg.Vars {
		fnc(v.Decl)
	}
	for _, f := range pkg.Funcs {
		fnc(f.Decl)
	}
	for _, t := range pkg.Types {
		fnc(t.Decl)
		for _, c := range t.Consts {
			fnc(c.Decl)
		}
		for _, v := range t.Vars {
			fnc(v.Decl)
		}
		for _, f := range t.Funcs {
			fnc(f.Decl)
		}
		for _, m := range t.Methods {
			fnc(m.Decl)
		}
	}
}

// packageIDs is a collection of top-level package identifiers
// in the package being rendered and any related packages.
type packageIDs struct {
	// name is the name of the package being rendered.
	//
	// E.g., "json"
	name string

	// impPaths maps package names to their import paths.
	//
	// E.g., impPaths["json"] == "encoding/json"
	impPaths map[string]string // map[name]pkgPath

	// pkgIDs is the set of all top-level identifiers in this package and
	// any related package.
	//
	// E.g., pkgIDs["json"]["Encoder.Encode"] == true
	pkgIDs map[string]map[string]bool // map[name]map[topLevelID]bool

	// topLevelDecls is the set of all AST declarations for the this package.
	topLevelDecls map[interface{}]bool // map[T]bool where T is *ast.FuncDecl | *ast.GenDecl | *ast.TypeSpec | *ast.ValueSpec
}

// newPackageIDs returns a packageIDs that collects all top-level identifiers
// for the given package pkg and any related packages.
func newPackageIDs(pkg *doc.Package, related ...*doc.Package) *packageIDs {
	pids := &packageIDs{
		name:          pkg.Name,
		impPaths:      make(map[string]string),
		pkgIDs:        make(map[string]map[string]bool),
		topLevelDecls: make(map[interface{}]bool),
	}

	// Collect top-level declaration IDs for pkg and related packages.
	for _, pkg := range append([]*doc.Package{pkg}, related...) {
		if _, ok := pids.pkgIDs[pkg.Name]; ok {
			continue // package name conflicts, ignore this package
		}
		pids.impPaths[pkg.Name] = pkg.ImportPath
		pids.pkgIDs[pkg.Name] = make(map[string]bool)
		forEachPackageDecl(pkg, func(decl ast.Decl) {
			for _, id := range generateAnchorPoints(decl) {
				pids.pkgIDs[pkg.Name][id] = true // E.g., ["io]["Reader.Read"]
			}
		})
	}

	// Collect AST objects for accurate linking of Go source code.
	forEachPackageDecl(pkg, func(decl ast.Decl) {
		pids.topLevelDecls[decl] = true
		if gd, _ := decl.(*ast.GenDecl); gd != nil {
			for _, sp := range gd.Specs {
				pids.topLevelDecls[sp] = true
			}
		}
	})
	return pids
}

// declIDs is a collection of identifiers that are related to the ast.Decl
// currently being processed. Using Decl-level variables allows us to provide
// greater accuracy in linking when comments refer to the variable names.
//
// For example, we can link "r.Read" to "io.Reader.Read" because we know that
// variable "r" is of type "io.Reader", which has a "Read" method.
type declIDs struct {
	// recvType is the type of the receiver for any methods.
	//
	// E.g., "Reader"
	recvType string

	// paramTypes is a mapping of parameter names in a ast.FuncDecl
	// to the type of that parameter.
	//
	// E.g., paramTypes["r"] == "io.Reader"
	paramTypes map[string]string // map[varName]typeName
}

func newDeclIDs(decl ast.Decl) *declIDs {
	dids := &declIDs{paramTypes: make(map[string]string)}

	switch decl := decl.(type) {
	case *ast.GenDecl:
		// Note that go/doc.New automatically splits type declaration
		// blocks into individual specifications.
		// If there are multiple, it okay to skip this logic, since
		// all of this information is just to improve the heuristics of toHTML.
		if decl.Tok == token.TYPE && len(decl.Specs) == 1 {
			dids.recvType = decl.Specs[0].(*ast.TypeSpec).Name.String()
		}
	case *ast.FuncDecl:
		// Obtain receiver variable and type names.
		if decl.Recv != nil && len(decl.Recv.List) > 0 {
			f := decl.Recv.List[0]
			dids.recvType, _ = nodeName(f.Type) // E.g., "Reader"
			if len(f.Names) == 1 && dids.recvType != "" {
				varName, _ := nodeName(f.Names[0]) // E.g., "r"
				if varName != "" {
					dids.paramTypes[varName] = dids.recvType
				}
			}
		}

		// Add mapping of variable names to types names for parameters and results.
		for _, flist := range []*ast.FieldList{decl.Type.Params, decl.Type.Results} {
			if flist == nil {
				continue
			}
			for _, field := range flist.List {
				typeName, _ := nodeName(field.Type) // E.g., "context.Context"
				if typeName == "" {
					continue
				}
				for _, name := range field.Names {
					varName, _ := nodeName(name) // E.g., "ctx"
					if varName != "" {
						dids.paramTypes[varName] = typeName
					}
				}
			}
		}
	}
	return dids
}

type identifierResolver struct {
	*packageIDs
	*declIDs

	// packageURL is a function for producing URLs from package paths.
	//
	// E.g., packageURL("builtin") == "/pkg/builtin/index.html"
	packageURL func(string) string
}

// toURL returns a URL to locate the given package, and
// optionally a specific identifier in that package.
// The pkgPath may be empty, indicating that this is an anchor only URL.
// The id may be empty, indicating that this refers to the package itself.
func (r identifierResolver) toURL(pkgPath, id string) (url string) {
	if pkgPath != "" {
		url = "/" + pkgPath
		if r.packageURL != nil {
			url = r.packageURL(pkgPath)
		}
	}
	if id != "" {
		url += "#" + id
	}
	return url
}

// toHTML formats a dot-delimited word as HTML with each ID segment converted
// to be a link to the relevant declaration.
func (r identifierResolver) toHTML(word string) string {
	// extraSuffix is extra identifier segments that can't be matched
	// probably because we lack type information.
	var extraSuffix string // E.g., ".Get" for an unknown Get method
	origIDs := strings.Split(word, ".")

	// Skip any standalone unexported identifier.
	if !isExported(word) && len(origIDs) == 1 {
		return template.HTMLEscapeString(word)
	}

	// Generate variations on the original word.
	var altWords []string
	if vtype, ok := r.paramTypes[origIDs[0]]; ok {
		altWords = append(altWords, vtype+word[len(origIDs[0]):]) // E.g., "r.Read" => "io.Reader.Read"
	} else if r.recvType != "" {
		altWords = append(altWords, r.recvType+"."+word) // E.g., "Read" => "Reader.Read"
	}
	altWords = append(altWords, word)

	var altWord string
	for _, s := range altWords {
		if _, _, ok := r.lookup(s); ok {
			altWord = s // direct match
			goto linkify
		}
		if _, _, ok := r.lookup(strings.TrimSuffix(s, "s")); ok {
			altWord = s[:len(s)-len("s")] // E.g., "Caches" => "Cache"
			goto linkify
		}
		if _, _, ok := r.lookup(strings.TrimSuffix(s, "es")); ok {
			altWord = s[:len(s)-len("es")] // E.g., "Boxes" => "Box"
			goto linkify
		}

		// Repeatedly truncate the last segment, searching for a partial match.
		i, j := len(s), len(word)
		for i >= 0 && j >= 0 {
			allowPartial := isExported(word[:j]) || strings.Contains(word[:j], ".")
			if _, _, ok := r.lookup(s[:i]); ok && allowPartial {
				altWord = s[:i]
				origIDs = strings.Split(word[:j], ".")
				extraSuffix = word[j:]
				goto linkify
			}
			i = strings.LastIndexByte(s[:i], '.')
			j = strings.LastIndexByte(word[:j], '.')
		}
	}
	return template.HTMLEscapeString(word) // no match found

linkify:
	// altWord contains a modified dot-separated identifier.
	// origIDs contains the segments of the original identifier.
	// It is possible for Split(altWord, ".") to be longer than origIDs
	// if an implicit type was prepended.
	// E.g., altWord="io.Reader.Read", origIDs=["r", "Read"]

	// Skip past implicit prefix selectors.
	// E.g., i=3, altWord[i:]="Reader.Read"
	var i int
	for strings.Count(altWord[i:], ".")+1 != len(origIDs) {
		i += strings.IndexByte(altWord[i:], '.') + 1
	}

	var outs []string
	for _, s := range origIDs {
		// Advance to the next segment in altWord.
		// E.g., i=9,  altWord[:i]="io.Reader",      s="r"
		// E.g., i=14, altWord[:i]="io.Reader.Read", s="Read"
		if n := strings.IndexByte(altWord[i+1:], '.'); n >= 0 {
			i += 1 + n
		} else {
			i = len(altWord)
		}

		path, name, _ := r.lookup(altWord[:i])
		u := r.toURL(path, name)
		u = template.HTMLEscapeString(u)
		s = template.HTMLEscapeString(s)
		outs = append(outs, fmt.Sprintf(`<a href="%s">%s</a>`, u, s))
	}
	return strings.Join(outs, ".") + extraSuffix
}

// lookup looks up a dot-separated identifier.
// E.g., "pkg", "pkg.Var", "Recv.Method", "Struct.Field", "pkg.Struct.Field"
func (r identifierResolver) lookup(id string) (pkgPath, name string, ok bool) {
	if r.pkgIDs[r.name][id] {
		return "", id, true // ID refers to local top-level declaration
	}
	if path := r.impPaths[id]; path != "" {
		return path, "", true // ID refers to a package
	}
	if i := strings.IndexByte(id, '.'); i >= 0 {
		prefix, suffix := id[:i], id[i+1:]
		if r.pkgIDs[prefix][suffix] {
			if prefix == r.name {
				prefix = ""
			}
			return r.impPaths[prefix], suffix, true // ID refers to a different package's top-level declaration
		}
	}
	return "", "", false // not found
}

func nodeName(n ast.Node) (string, *ast.Ident) {
	switch n := n.(type) {
	case *ast.Ident:
		return n.String(), n
	case *ast.StarExpr:
		return nodeName(n.X)
	case *ast.SelectorExpr:
		if prefix, _ := nodeName(n.X); prefix != "" {
			return prefix + "." + n.Sel.String(), n.Sel
		}
		return n.Sel.String(), n.Sel
	default:
		return "", nil
	}
}

func isExported(id string) bool {
	r, _ := utf8.DecodeRuneInString(id)
	return unicode.IsUpper(r)
}
