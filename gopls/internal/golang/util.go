// Copyright 2019 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package golang

import (
	"context"
	"go/ast"
	"go/printer"
	"go/token"
	"go/types"
	"regexp"
	"strings"
	"unicode"

	"golang.org/x/tools/gopls/internal/cache"
	"golang.org/x/tools/gopls/internal/cache/metadata"
	"golang.org/x/tools/gopls/internal/cache/parsego"
	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/gopls/internal/util/astutil"
	"golang.org/x/tools/gopls/internal/util/bug"
	"golang.org/x/tools/gopls/internal/util/safetoken"
	"golang.org/x/tools/internal/tokeninternal"
)

// IsGenerated gets and reads the file denoted by uri and reports
// whether it contains a "generated file" comment as described at
// https://golang.org/s/generatedcode.
//
// TODO(adonovan): opt: this function does too much.
// Move snapshot.ReadFile into the caller (most of which have already done it).
func IsGenerated(ctx context.Context, snapshot *cache.Snapshot, uri protocol.DocumentURI) bool {
	fh, err := snapshot.ReadFile(ctx, uri)
	if err != nil {
		return false
	}
	pgf, err := snapshot.ParseGo(ctx, fh, parsego.Header)
	if err != nil {
		return false
	}
	for _, commentGroup := range pgf.File.Comments {
		for _, comment := range commentGroup.List {
			if matched := generatedRx.MatchString(comment.Text); matched {
				// Check if comment is at the beginning of the line in source.
				if safetoken.Position(pgf.Tok, comment.Slash).Column == 1 {
					return true
				}
			}
		}
	}
	return false
}

// adjustedObjEnd returns the end position of obj, possibly modified for
// package names.
//
// TODO(rfindley): eliminate this function, by inlining it at callsites where
// it makes sense.
func adjustedObjEnd(obj types.Object) token.Pos {
	nameLen := len(obj.Name())
	if pkgName, ok := obj.(*types.PkgName); ok {
		// An imported Go package has a package-local, unqualified name.
		// When the name matches the imported package name, there is no
		// identifier in the import spec with the local package name.
		//
		// For example:
		// 		import "go/ast" 	// name "ast" matches package name
		// 		import a "go/ast"  	// name "a" does not match package name
		//
		// When the identifier does not appear in the source, have the range
		// of the object be the import path, including quotes.
		if pkgName.Imported().Name() == pkgName.Name() {
			nameLen = len(pkgName.Imported().Path()) + len(`""`)
		}
	}
	return obj.Pos() + token.Pos(nameLen)
}

// Matches cgo generated comment as well as the proposed standard:
//
//	https://golang.org/s/generatedcode
var generatedRx = regexp.MustCompile(`// .*DO NOT EDIT\.?`)

// FormatNode returns the "pretty-print" output for an ast node.
func FormatNode(fset *token.FileSet, n ast.Node) string {
	var buf strings.Builder
	if err := printer.Fprint(&buf, fset, n); err != nil {
		// TODO(rfindley): we should use bug.Reportf here.
		// We encounter this during completion.resolveInvalid.
		return ""
	}
	return buf.String()
}

// formatNodeFile is like FormatNode, but requires only the token.File for the
// syntax containing the given ast node.
func formatNodeFile(file *token.File, n ast.Node) string {
	fset := tokeninternal.FileSetFor(file)
	return FormatNode(fset, n)
}

// findFileInDeps finds package metadata containing URI in the transitive
// dependencies of m. When using the Go command, the answer is unique.
func findFileInDeps(s metadata.Source, mp *metadata.Package, uri protocol.DocumentURI) *metadata.Package {
	seen := make(map[PackageID]bool)
	var search func(*metadata.Package) *metadata.Package
	search = func(mp *metadata.Package) *metadata.Package {
		if seen[mp.ID] {
			return nil
		}
		seen[mp.ID] = true
		for _, cgf := range mp.CompiledGoFiles {
			if cgf == uri {
				return mp
			}
		}
		for _, dep := range mp.DepsByPkgPath {
			mp := s.Metadata(dep)
			if mp == nil {
				bug.Reportf("nil metadata for %q", dep)
				continue
			}
			if found := search(mp); found != nil {
				return found
			}
		}
		return nil
	}
	return search(mp)
}

// CollectScopes returns all scopes in an ast path, ordered as innermost scope
// first.
//
// TODO(adonovan): move this to golang/completion and simplify to use
// Scopes.Innermost and LookupParent instead.
func CollectScopes(info *types.Info, path []ast.Node, pos token.Pos) []*types.Scope {
	// scopes[i], where i<len(path), is the possibly nil Scope of path[i].
	var scopes []*types.Scope
	for _, n := range path {
		// Include *FuncType scope if pos is inside the function body.
		switch node := n.(type) {
		case *ast.FuncDecl:
			if node.Body != nil && astutil.NodeContains(node.Body, pos) {
				n = node.Type
			}
		case *ast.FuncLit:
			if node.Body != nil && astutil.NodeContains(node.Body, pos) {
				n = node.Type
			}
		}
		scopes = append(scopes, info.Scopes[n])
	}
	return scopes
}

// requalifier returns a function that re-qualifies identifiers and qualified
// identifiers contained in targetFile using the given metadata qualifier.
func requalifier(s metadata.Source, targetFile *ast.File, targetMeta *metadata.Package, mq MetadataQualifier) func(string) string {
	qm := map[string]string{
		"": mq(targetMeta.Name, "", targetMeta.PkgPath),
	}

	// Construct mapping of import paths to their defined or implicit names.
	for _, imp := range targetFile.Imports {
		name, pkgName, impPath, pkgPath := importInfo(s, imp, targetMeta)

		// Re-map the target name for the source file.
		qm[name] = mq(pkgName, impPath, pkgPath)
	}

	return func(name string) string {
		if newName, ok := qm[name]; ok {
			return newName
		}
		return name
	}
}

// A MetadataQualifier is a function that qualifies an identifier declared in a
// package with the given package name, import path, and package path.
//
// In scenarios where metadata is missing the provided PackageName and
// PackagePath may be empty, but ImportPath must always be non-empty.
type MetadataQualifier func(PackageName, ImportPath, PackagePath) string

// MetadataQualifierForFile returns a metadata qualifier that chooses the best
// qualification of an imported package relative to the file f in package with
// metadata m.
func MetadataQualifierForFile(s metadata.Source, f *ast.File, mp *metadata.Package) MetadataQualifier {
	// Record local names for import paths.
	localNames := make(map[ImportPath]string) // local names for imports in f
	for _, imp := range f.Imports {
		name, _, impPath, _ := importInfo(s, imp, mp)
		localNames[impPath] = name
	}

	// Record a package path -> import path mapping.
	inverseDeps := make(map[PackageID]PackagePath)
	for path, id := range mp.DepsByPkgPath {
		inverseDeps[id] = path
	}
	importsByPkgPath := make(map[PackagePath]ImportPath) // best import paths by pkgPath
	for impPath, id := range mp.DepsByImpPath {
		if id == "" {
			continue
		}
		pkgPath := inverseDeps[id]
		_, hasPath := importsByPkgPath[pkgPath]
		_, hasImp := localNames[impPath]
		// In rare cases, there may be multiple import paths with the same package
		// path. In such scenarios, prefer an import path that already exists in
		// the file.
		if !hasPath || hasImp {
			importsByPkgPath[pkgPath] = impPath
		}
	}

	return func(pkgName PackageName, impPath ImportPath, pkgPath PackagePath) string {
		// If supplied, translate the package path to an import path in the source
		// package.
		if pkgPath != "" {
			if srcImp := importsByPkgPath[pkgPath]; srcImp != "" {
				impPath = srcImp
			}
			if pkgPath == mp.PkgPath {
				return ""
			}
		}
		if localName, ok := localNames[impPath]; ok && impPath != "" {
			return localName
		}
		if pkgName != "" {
			return string(pkgName)
		}
		idx := strings.LastIndexByte(string(impPath), '/')
		return string(impPath[idx+1:])
	}
}

// importInfo collects information about the import specified by imp,
// extracting its file-local name, package name, import path, and package path.
//
// If metadata is missing for the import, the resulting package name and
// package path may be empty, and the file local name may be guessed based on
// the import path.
//
// Note: previous versions of this helper used a PackageID->PackagePath map
// extracted from m, for extracting package path even in the case where
// metadata for a dep was missing. This should not be necessary, as we should
// always have metadata for IDs contained in DepsByPkgPath.
func importInfo(s metadata.Source, imp *ast.ImportSpec, mp *metadata.Package) (string, PackageName, ImportPath, PackagePath) {
	var (
		name    string // local name
		pkgName PackageName
		impPath = metadata.UnquoteImportPath(imp)
		pkgPath PackagePath
	)

	// If the import has a local name, use it.
	if imp.Name != nil {
		name = imp.Name.Name
	}

	// Try to find metadata for the import. If successful and there is no local
	// name, the package name is the local name.
	if depID := mp.DepsByImpPath[impPath]; depID != "" {
		if depMP := s.Metadata(depID); depMP != nil {
			if name == "" {
				name = string(depMP.Name)
			}
			pkgName = depMP.Name
			pkgPath = depMP.PkgPath
		}
	}

	// If the local name is still unknown, guess it based on the import path.
	if name == "" {
		idx := strings.LastIndexByte(string(impPath), '/')
		name = string(impPath[idx+1:])
	}
	return name, pkgName, impPath, pkgPath
}

// isDirective reports whether c is a comment directive.
//
// Copied and adapted from go/src/go/ast/ast.go.
func isDirective(c string) bool {
	if len(c) < 3 {
		return false
	}
	if c[1] != '/' {
		return false
	}
	//-style comment (no newline at the end)
	c = c[2:]
	if len(c) == 0 {
		// empty line
		return false
	}
	// "//line " is a line directive.
	// (The // has been removed.)
	if strings.HasPrefix(c, "line ") {
		return true
	}

	// "//[a-z0-9]+:[a-z0-9]"
	// (The // has been removed.)
	colon := strings.Index(c, ":")
	if colon <= 0 || colon+1 >= len(c) {
		return false
	}
	for i := 0; i <= colon+1; i++ {
		if i == colon {
			continue
		}
		b := c[i]
		if !('a' <= b && b <= 'z' || '0' <= b && b <= '9') {
			return false
		}
	}
	return true
}

// embeddedIdent returns the type name identifier for an embedding x, if x in a
// valid embedding. Otherwise, it returns nil.
//
// Spec: An embedded field must be specified as a type name T or as a pointer
// to a non-interface type name *T
func embeddedIdent(x ast.Expr) *ast.Ident {
	if star, ok := x.(*ast.StarExpr); ok {
		x = star.X
	}
	switch ix := x.(type) { // check for instantiated receivers
	case *ast.IndexExpr:
		x = ix.X
	case *ast.IndexListExpr:
		x = ix.X
	}
	switch x := x.(type) {
	case *ast.Ident:
		return x
	case *ast.SelectorExpr:
		if _, ok := x.X.(*ast.Ident); ok {
			return x.Sel
		}
	}
	return nil
}

// An importFunc is an implementation of the single-method
// types.Importer interface based on a function value.
type ImporterFunc func(path string) (*types.Package, error)

func (f ImporterFunc) Import(path string) (*types.Package, error) { return f(path) }

// isBuiltin reports whether obj is a built-in symbol (e.g. append, iota, error.Error, unsafe.Slice).
// All other symbols have a valid position and a valid package.
func isBuiltin(obj types.Object) bool { return !obj.Pos().IsValid() }

// btoi returns int(b) as proposed in #64825.
func btoi(b bool) int {
	if b {
		return 1
	} else {
		return 0
	}
}

// AbbreviateVarName returns an abbreviated var name based on the given full
// name (which may be a type name, for example).
//
// See the simple heuristics documented in line.
func AbbreviateVarName(s string) string {
	var (
		b            strings.Builder
		useNextUpper bool
	)
	for i, r := range s {
		// Stop if we encounter a non-identifier rune.
		if !unicode.IsLetter(r) && !unicode.IsNumber(r) {
			break
		}

		// Otherwise, take the first letter from word boundaries, assuming
		// camelCase.
		if i == 0 {
			b.WriteRune(unicode.ToLower(r))
		}

		if unicode.IsUpper(r) {
			if useNextUpper {
				b.WriteRune(unicode.ToLower(r))
				useNextUpper = false
			}
		} else {
			useNextUpper = true
		}
	}
	return b.String()
}

// copyrightComment returns the copyright comment group from the input file, or
// nil if not found.
func copyrightComment(file *ast.File) *ast.CommentGroup {
	if len(file.Comments) == 0 {
		return nil
	}

	// Copyright should appear before package decl and must be the first
	// comment group.
	if c := file.Comments[0]; c.Pos() < file.Package && c != file.Doc &&
		!isDirective(c.List[0].Text) &&
		strings.Contains(strings.ToLower(c.List[0].Text), "copyright") {
		return c
	}

	return nil
}

var buildConstraintRe = regexp.MustCompile(`^//(go:build|\s*\+build).*`)

// buildConstraintComment returns the build constraint comment from the input
// file.
// Returns nil if not found.
func buildConstraintComment(file *ast.File) *ast.Comment {
	for _, cg := range file.Comments {
		// In Go files a build constraint must appear before the package clause.
		// See https://pkg.go.dev/cmd/go#hdr-Build_constraints
		if cg.Pos() > file.Package {
			return nil
		}

		for _, c := range cg.List {
			// TODO: use ast.ParseDirective when available (#68021).
			if buildConstraintRe.MatchString(c.Text) {
				return c
			}
		}
	}

	return nil
}
