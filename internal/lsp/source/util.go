// Copyright 2019 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package source

import (
	"context"
	"fmt"
	"go/ast"
	"go/printer"
	"go/token"
	"go/types"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/mod/modfile"
	"golang.org/x/tools/internal/lsp/bug"
	"golang.org/x/tools/internal/lsp/protocol"
	"golang.org/x/tools/internal/span"
)

// MappedRange provides mapped protocol.Range for a span.Range, accounting for
// UTF-16 code points.
type MappedRange struct {
	spanRange span.Range             // the range in the compiled source (package.CompiledGoFiles)
	m         *protocol.ColumnMapper // a mapper of the edited source (package.GoFiles)
}

// NewMappedRange returns a MappedRange for the given start and end token.Pos.
//
// By convention, start and end are assumed to be positions in the compiled (==
// type checked) source, whereas the column mapper m maps positions in the
// user-edited source. Note that these may not be the same, as when using CGo:
// CompiledGoFiles contains generated files, whose positions (via
// token.File.Position) point to locations in the edited file -- the file
// containing `import "C"`.
func NewMappedRange(fset *token.FileSet, m *protocol.ColumnMapper, start, end token.Pos) MappedRange {
	if tf := fset.File(start); tf == nil {
		bug.Report("nil file", nil)
	} else {
		mapped := m.TokFile.Name()
		adjusted := tf.PositionFor(start, true) // adjusted position
		if adjusted.Filename != mapped {
			bug.Reportf("mapped file %q does not match start position file %q", mapped, adjusted.Filename)
		}
	}
	return MappedRange{
		spanRange: span.NewRange(fset, start, end),
		m:         m,
	}
}

// Range returns the LSP range in the edited source.
//
// See the documentation of NewMappedRange for information on edited vs
// compiled source.
func (s MappedRange) Range() (protocol.Range, error) {
	if s.m == nil {
		return protocol.Range{}, bug.Errorf("invalid range")
	}
	spn, err := s.Span()
	if err != nil {
		return protocol.Range{}, err
	}
	return s.m.Range(spn)
}

// Span returns the span corresponding to the mapped range in the edited
// source.
//
// See the documentation of NewMappedRange for information on edited vs
// compiled source.
func (s MappedRange) Span() (span.Span, error) {
	// In the past, some code-paths have relied on Span returning an error if s
	// is the zero value (i.e. s.m is nil). But this should be treated as a bug:
	// observe that s.URI() would panic in this case.
	if s.m == nil {
		return span.Span{}, bug.Errorf("invalid range")
	}
	return span.FileSpan(s.spanRange.TokFile, s.m.TokFile, s.spanRange.Start, s.spanRange.End)
}

// URI returns the URI of the edited file.
//
// See the documentation of NewMappedRange for information on edited vs
// compiled source.
func (s MappedRange) URI() span.URI {
	return s.m.URI
}

// GetParsedFile is a convenience function that extracts the Package and
// ParsedGoFile for a file in a Snapshot. pkgPolicy is one of NarrowestPackage/
// WidestPackage.
func GetParsedFile(ctx context.Context, snapshot Snapshot, fh FileHandle, pkgPolicy PackageFilter) (Package, *ParsedGoFile, error) {
	pkg, err := snapshot.PackageForFile(ctx, fh.URI(), TypecheckWorkspace, pkgPolicy)
	if err != nil {
		return nil, nil, err
	}
	pgh, err := pkg.File(fh.URI())
	return pkg, pgh, err
}

func IsGenerated(ctx context.Context, snapshot Snapshot, uri span.URI) bool {
	fh, err := snapshot.GetFile(ctx, uri)
	if err != nil {
		return false
	}
	pgf, err := snapshot.ParseGo(ctx, fh, ParseHeader)
	if err != nil {
		return false
	}
	for _, commentGroup := range pgf.File.Comments {
		for _, comment := range commentGroup.List {
			if matched := generatedRx.MatchString(comment.Text); matched {
				// Check if comment is at the beginning of the line in source.
				if pgf.Tok.Position(comment.Slash).Column == 1 {
					return true
				}
			}
		}
	}
	return false
}

func nodeToProtocolRange(snapshot Snapshot, pkg Package, n ast.Node) (protocol.Range, error) {
	mrng, err := posToMappedRange(snapshot, pkg, n.Pos(), n.End())
	if err != nil {
		return protocol.Range{}, err
	}
	return mrng.Range()
}

func objToMappedRange(snapshot Snapshot, pkg Package, obj types.Object) (MappedRange, error) {
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
			return posToMappedRange(snapshot, pkg, obj.Pos(), obj.Pos()+token.Pos(len(pkgName.Imported().Path())+2))
		}
	}
	return nameToMappedRange(snapshot, pkg, obj.Pos(), obj.Name())
}

func nameToMappedRange(snapshot Snapshot, pkg Package, pos token.Pos, name string) (MappedRange, error) {
	return posToMappedRange(snapshot, pkg, pos, pos+token.Pos(len(name)))
}

func posToMappedRange(snapshot Snapshot, pkg Package, pos, end token.Pos) (MappedRange, error) {
	logicalFilename := snapshot.FileSet().File(pos).Position(pos).Filename
	pgf, _, err := findFileInDeps(pkg, span.URIFromPath(logicalFilename))
	if err != nil {
		return MappedRange{}, err
	}
	if !pos.IsValid() {
		return MappedRange{}, fmt.Errorf("invalid position for %v", pos)
	}
	if !end.IsValid() {
		return MappedRange{}, fmt.Errorf("invalid position for %v", end)
	}
	return NewMappedRange(snapshot.FileSet(), pgf.Mapper, pos, end), nil
}

// Matches cgo generated comment as well as the proposed standard:
//
//	https://golang.org/s/generatedcode
var generatedRx = regexp.MustCompile(`// .*DO NOT EDIT\.?`)

// FileKindForLang returns the file kind associated with the given language ID,
// or UnknownKind if the language ID is not recognized.
func FileKindForLang(langID string) FileKind {
	switch langID {
	case "go":
		return Go
	case "go.mod":
		return Mod
	case "go.sum":
		return Sum
	case "tmpl", "gotmpl":
		return Tmpl
	case "go.work":
		return Work
	default:
		return UnknownKind
	}
}

func (k FileKind) String() string {
	switch k {
	case Go:
		return "go"
	case Mod:
		return "go.mod"
	case Sum:
		return "go.sum"
	case Tmpl:
		return "tmpl"
	case Work:
		return "go.work"
	default:
		return fmt.Sprintf("unk%d", k)
	}
}

// nodeAtPos returns the index and the node whose position is contained inside
// the node list.
func nodeAtPos(nodes []ast.Node, pos token.Pos) (ast.Node, int) {
	if nodes == nil {
		return nil, -1
	}
	for i, node := range nodes {
		if node.Pos() <= pos && pos <= node.End() {
			return node, i
		}
	}
	return nil, -1
}

// IsInterface returns if a types.Type is an interface
func IsInterface(T types.Type) bool {
	return T != nil && types.IsInterface(T)
}

// FormatNode returns the "pretty-print" output for an ast node.
func FormatNode(fset *token.FileSet, n ast.Node) string {
	var buf strings.Builder
	if err := printer.Fprint(&buf, fset, n); err != nil {
		return ""
	}
	return buf.String()
}

// Deref returns a pointer's element type, traversing as many levels as needed.
// Otherwise it returns typ.
//
// It can return a pointer type for cyclic types (see golang/go#45510).
func Deref(typ types.Type) types.Type {
	var seen map[types.Type]struct{}
	for {
		p, ok := typ.Underlying().(*types.Pointer)
		if !ok {
			return typ
		}
		if _, ok := seen[p.Elem()]; ok {
			return typ
		}

		typ = p.Elem()

		if seen == nil {
			seen = make(map[types.Type]struct{})
		}
		seen[typ] = struct{}{}
	}
}

func SortDiagnostics(d []*Diagnostic) {
	sort.Slice(d, func(i int, j int) bool {
		return CompareDiagnostic(d[i], d[j]) < 0
	})
}

func CompareDiagnostic(a, b *Diagnostic) int {
	if r := protocol.CompareRange(a.Range, b.Range); r != 0 {
		return r
	}
	if a.Source < b.Source {
		return -1
	}
	if a.Source > b.Source {
		return +1
	}
	if a.Message < b.Message {
		return -1
	}
	if a.Message > b.Message {
		return +1
	}
	return 0
}

// FindPackageFromPos finds the first package containing pos in its
// type-checked AST.
func FindPackageFromPos(ctx context.Context, snapshot Snapshot, pos token.Pos) (Package, error) {
	tok := snapshot.FileSet().File(pos)
	if tok == nil {
		return nil, fmt.Errorf("no file for pos %v", pos)
	}
	uri := span.URIFromPath(tok.Name())
	pkgs, err := snapshot.PackagesForFile(ctx, uri, TypecheckAll, true)
	if err != nil {
		return nil, err
	}
	// Only return the package if it actually type-checked the given position.
	for _, pkg := range pkgs {
		parsed, err := pkg.File(uri)
		if err != nil {
			return nil, err
		}
		if parsed == nil {
			continue
		}
		if parsed.Tok.Base() != tok.Base() {
			continue
		}
		return pkg, nil
	}
	return nil, fmt.Errorf("no package for given file position")
}

// findFileInDeps finds uri in pkg or its dependencies.
func findFileInDeps(pkg Package, uri span.URI) (*ParsedGoFile, Package, error) {
	queue := []Package{pkg}
	seen := make(map[string]bool)

	for len(queue) > 0 {
		pkg := queue[0]
		queue = queue[1:]
		seen[pkg.ID()] = true

		if pgf, err := pkg.File(uri); err == nil {
			return pgf, pkg, nil
		}
		for _, dep := range pkg.Imports() {
			if !seen[dep.ID()] {
				queue = append(queue, dep)
			}
		}
	}
	return nil, nil, fmt.Errorf("no file for %s in package %s", uri, pkg.ID())
}

// ImportPath returns the unquoted import path of s,
// or "" if the path is not properly quoted.
func ImportPath(s *ast.ImportSpec) string {
	t, err := strconv.Unquote(s.Path.Value)
	if err != nil {
		return ""
	}
	return t
}

// NodeContains returns true if a node encloses a given position pos.
func NodeContains(n ast.Node, pos token.Pos) bool {
	return n != nil && n.Pos() <= pos && pos <= n.End()
}

// CollectScopes returns all scopes in an ast path, ordered as innermost scope
// first.
func CollectScopes(info *types.Info, path []ast.Node, pos token.Pos) []*types.Scope {
	// scopes[i], where i<len(path), is the possibly nil Scope of path[i].
	var scopes []*types.Scope
	for _, n := range path {
		// Include *FuncType scope if pos is inside the function body.
		switch node := n.(type) {
		case *ast.FuncDecl:
			if node.Body != nil && NodeContains(node.Body, pos) {
				n = node.Type
			}
		case *ast.FuncLit:
			if node.Body != nil && NodeContains(node.Body, pos) {
				n = node.Type
			}
		}
		scopes = append(scopes, info.Scopes[n])
	}
	return scopes
}

// Qualifier returns a function that appropriately formats a types.PkgName
// appearing in a *ast.File.
func Qualifier(f *ast.File, pkg *types.Package, info *types.Info) types.Qualifier {
	// Construct mapping of import paths to their defined or implicit names.
	imports := make(map[*types.Package]string)
	for _, imp := range f.Imports {
		var obj types.Object
		if imp.Name != nil {
			obj = info.Defs[imp.Name]
		} else {
			obj = info.Implicits[imp]
		}
		if pkgname, ok := obj.(*types.PkgName); ok {
			imports[pkgname.Imported()] = pkgname.Name()
		}
	}
	// Define qualifier to replace full package paths with names of the imports.
	return func(p *types.Package) string {
		if p == pkg {
			return ""
		}
		if name, ok := imports[p]; ok {
			if name == "." {
				return ""
			}
			return name
		}
		return p.Name()
	}
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

// honorSymlinks toggles whether or not we consider symlinks when comparing
// file or directory URIs.
const honorSymlinks = false

func CompareURI(left, right span.URI) int {
	if honorSymlinks {
		return span.CompareURI(left, right)
	}
	if left == right {
		return 0
	}
	if left < right {
		return -1
	}
	return 1
}

// InDir checks whether path is in the file tree rooted at dir.
// InDir makes some effort to succeed even in the presence of symbolic links.
//
// Copied and slightly adjusted from go/src/cmd/go/internal/search/search.go.
func InDir(dir, path string) bool {
	if inDirLex(dir, path) {
		return true
	}
	if !honorSymlinks {
		return false
	}
	xpath, err := filepath.EvalSymlinks(path)
	if err != nil || xpath == path {
		xpath = ""
	} else {
		if inDirLex(dir, xpath) {
			return true
		}
	}

	xdir, err := filepath.EvalSymlinks(dir)
	if err == nil && xdir != dir {
		if inDirLex(xdir, path) {
			return true
		}
		if xpath != "" {
			if inDirLex(xdir, xpath) {
				return true
			}
		}
	}
	return false
}

// inDirLex is like inDir but only checks the lexical form of the file names.
// It does not consider symbolic links.
//
// Copied from go/src/cmd/go/internal/search/search.go.
func inDirLex(dir, path string) bool {
	pv := strings.ToUpper(filepath.VolumeName(path))
	dv := strings.ToUpper(filepath.VolumeName(dir))
	path = path[len(pv):]
	dir = dir[len(dv):]
	switch {
	default:
		return false
	case pv != dv:
		return false
	case len(path) == len(dir):
		if path == dir {
			return true
		}
		return false
	case dir == "":
		return path != ""
	case len(path) > len(dir):
		if dir[len(dir)-1] == filepath.Separator {
			if path[:len(dir)] == dir {
				return path[len(dir):] != ""
			}
			return false
		}
		if path[len(dir)] == filepath.Separator && path[:len(dir)] == dir {
			if len(path) == len(dir)+1 {
				return true
			}
			return path[len(dir)+1:] != ""
		}
		return false
	}
}

// IsValidImport returns whether importPkgPath is importable
// by pkgPath
func IsValidImport(pkgPath, importPkgPath string) bool {
	i := strings.LastIndex(string(importPkgPath), "/internal/")
	if i == -1 {
		return true
	}
	if IsCommandLineArguments(string(pkgPath)) {
		return true
	}
	return strings.HasPrefix(string(pkgPath), string(importPkgPath[:i]))
}

// IsCommandLineArguments reports whether a given value denotes
// "command-line-arguments" package, which is a package with an unknown ID
// created by the go command. It can have a test variant, which is why callers
// should not check that a value equals "command-line-arguments" directly.
func IsCommandLineArguments(s string) bool {
	return strings.Contains(s, "command-line-arguments")
}

// LineToRange creates a Range spanning start and end.
func LineToRange(m *protocol.ColumnMapper, uri span.URI, start, end modfile.Position) (protocol.Range, error) {
	return ByteOffsetsToRange(m, uri, start.Byte, end.Byte)
}

// ByteOffsetsToRange creates a range spanning start and end.
func ByteOffsetsToRange(m *protocol.ColumnMapper, uri span.URI, start, end int) (protocol.Range, error) {
	line, col, err := span.ToPosition(m.TokFile, start)
	if err != nil {
		return protocol.Range{}, err
	}
	s := span.NewPoint(line, col, start)
	line, col, err = span.ToPosition(m.TokFile, end)
	if err != nil {
		return protocol.Range{}, err
	}
	e := span.NewPoint(line, col, end)
	return m.Range(span.New(uri, s, e))
}
