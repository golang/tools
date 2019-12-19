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
	"strings"

	"golang.org/x/tools/internal/lsp/protocol"
	"golang.org/x/tools/internal/span"
	errors "golang.org/x/xerrors"
)

type mappedRange struct {
	spanRange span.Range
	m         *protocol.ColumnMapper

	// protocolRange is the result of converting the spanRange using the mapper.
	// It is computed on-demand.
	protocolRange *protocol.Range
}

func newMappedRange(fset *token.FileSet, m *protocol.ColumnMapper, start, end token.Pos) mappedRange {
	return mappedRange{
		spanRange: span.Range{
			FileSet:   fset,
			Start:     start,
			End:       end,
			Converter: m.Converter,
		},
		m: m,
	}
}

func (s mappedRange) Range() (protocol.Range, error) {
	if s.protocolRange == nil {
		spn, err := s.spanRange.Span()
		if err != nil {
			return protocol.Range{}, err
		}
		prng, err := s.m.Range(spn)
		if err != nil {
			return protocol.Range{}, err
		}
		s.protocolRange = &prng
	}
	return *s.protocolRange, nil
}

func (s mappedRange) Span() (span.Span, error) {
	return s.spanRange.Span()
}

func (s mappedRange) URI() span.URI {
	return s.m.URI
}

// getParsedFile is a convenience function that extracts the Package and ParseGoHandle for a File in a Snapshot.
// selectPackage is typically Narrowest/WidestCheckPackageHandle below.
func getParsedFile(ctx context.Context, snapshot Snapshot, fh FileHandle, selectPackage PackagePolicy) (Package, ParseGoHandle, error) {
	phs, err := snapshot.PackageHandles(ctx, fh)
	if err != nil {
		return nil, nil, err
	}
	ph, err := selectPackage(phs)
	if err != nil {
		return nil, nil, err
	}
	pkg, err := ph.Check(ctx)
	if err != nil {
		return nil, nil, err
	}
	pgh, err := pkg.File(fh.Identity().URI)
	return pkg, pgh, err
}

type PackagePolicy func([]PackageHandle) (PackageHandle, error)

// NarrowestCheckPackageHandle picks the "narrowest" package for a given file.
//
// By "narrowest" package, we mean the package with the fewest number of files
// that includes the given file. This solves the problem of test variants,
// as the test will have more files than the non-test package.
func NarrowestCheckPackageHandle(handles []PackageHandle) (PackageHandle, error) {
	if len(handles) < 1 {
		return nil, errors.Errorf("no CheckPackageHandles")
	}
	result := handles[0]
	for _, handle := range handles[1:] {
		if result == nil || len(handle.CompiledGoFiles()) < len(result.CompiledGoFiles()) {
			result = handle
		}
	}
	if result == nil {
		return nil, errors.Errorf("nil CheckPackageHandles have been returned")
	}
	return result, nil
}

// WidestCheckPackageHandle returns the CheckPackageHandle containing the most files.
//
// This is useful for something like diagnostics, where we'd prefer to offer diagnostics
// for as many files as possible.
func WidestCheckPackageHandle(handles []PackageHandle) (PackageHandle, error) {
	if len(handles) < 1 {
		return nil, errors.Errorf("no CheckPackageHandles")
	}
	result := handles[0]
	for _, handle := range handles[1:] {
		if result == nil || len(handle.CompiledGoFiles()) > len(result.CompiledGoFiles()) {
			result = handle
		}
	}
	if result == nil {
		return nil, errors.Errorf("nil CheckPackageHandles have been returned")
	}
	return result, nil
}

// SpecificPackageHandle creates a PackagePolicy to select a
// particular PackageHandle when you alread know the one you want.
func SpecificPackageHandle(desiredID string) PackagePolicy {
	return func(handles []PackageHandle) (PackageHandle, error) {
		for _, h := range handles {
			if h.ID() == desiredID {
				return h, nil
			}
		}

		return nil, fmt.Errorf("no package handle with expected id %q", desiredID)
	}
}

func IsGenerated(ctx context.Context, view View, uri span.URI) bool {
	fh, err := view.Snapshot().GetFile(ctx, uri)
	if err != nil {
		return false
	}
	ph := view.Session().Cache().ParseGoHandle(fh, ParseHeader)
	parsed, _, _, err := ph.Parse(ctx)
	if err != nil {
		return false
	}
	tok := view.Session().Cache().FileSet().File(parsed.Pos())
	if tok == nil {
		return false
	}
	for _, commentGroup := range parsed.Comments {
		for _, comment := range commentGroup.List {
			if matched := generatedRx.MatchString(comment.Text); matched {
				// Check if comment is at the beginning of the line in source.
				if pos := tok.Position(comment.Slash); pos.Column == 1 {
					return true
				}
			}
		}
	}
	return false
}

func nodeToProtocolRange(ctx context.Context, view View, m *protocol.ColumnMapper, n ast.Node) (protocol.Range, error) {
	mrng, err := nodeToMappedRange(view, m, n)
	if err != nil {
		return protocol.Range{}, err
	}
	return mrng.Range()
}

func objToMappedRange(v View, pkg Package, obj types.Object) (mappedRange, error) {
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
			return posToMappedRange(v, pkg, obj.Pos(), obj.Pos()+token.Pos(len(pkgName.Imported().Path())+2))
		}
	}
	return nameToMappedRange(v, pkg, obj.Pos(), obj.Name())
}

func nameToMappedRange(v View, pkg Package, pos token.Pos, name string) (mappedRange, error) {
	return posToMappedRange(v, pkg, pos, pos+token.Pos(len(name)))
}

func nodeToMappedRange(view View, m *protocol.ColumnMapper, n ast.Node) (mappedRange, error) {
	return posToRange(view, m, n.Pos(), n.End())
}

func posToMappedRange(v View, pkg Package, pos, end token.Pos) (mappedRange, error) {
	logicalFilename := v.Session().Cache().FileSet().File(pos).Position(pos).Filename
	m, err := v.FindMapperInPackage(pkg, span.FileURI(logicalFilename))
	if err != nil {
		return mappedRange{}, err
	}
	return posToRange(v, m, pos, end)
}

func posToRange(view View, m *protocol.ColumnMapper, pos, end token.Pos) (mappedRange, error) {
	if !pos.IsValid() {
		return mappedRange{}, errors.Errorf("invalid position for %v", pos)
	}
	if !end.IsValid() {
		return mappedRange{}, errors.Errorf("invalid position for %v", end)
	}
	return newMappedRange(view.Session().Cache().FileSet(), m, pos, end), nil
}

// Matches cgo generated comment as well as the proposed standard:
//	https://golang.org/s/generatedcode
var generatedRx = regexp.MustCompile(`// .*DO NOT EDIT\.?`)

func DetectLanguage(langID, filename string) FileKind {
	switch langID {
	case "go":
		return Go
	case "go.mod":
		return Mod
	case "go.sum":
		return Sum
	}
	// Fallback to detecting the language based on the file extension.
	switch filepath.Ext(filename) {
	case ".mod":
		return Mod
	case ".sum":
		return Sum
	default: // fallback to Go
		return Go
	}
}

func (k FileKind) String() string {
	switch k {
	case Mod:
		return "go.mod"
	case Sum:
		return "go.sum"
	default:
		return "go"
	}
}

// Returns the index and the node whose position is contained inside the node list.
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

// indexExprAtPos returns the index of the expression containing pos.
func indexExprAtPos(pos token.Pos, args []ast.Expr) int {
	for i, expr := range args {
		if expr.Pos() <= pos && pos <= expr.End() {
			return i
		}
	}
	return len(args)
}

func exprAtPos(pos token.Pos, args []ast.Expr) ast.Expr {
	for _, expr := range args {
		if expr.Pos() <= pos && pos <= expr.End() {
			return expr
		}
	}
	return nil
}

// fieldSelections returns the set of fields that can
// be selected from a value of type T.
func fieldSelections(T types.Type) (fields []*types.Var) {
	// TODO(adonovan): this algorithm doesn't exclude ambiguous
	// selections that match more than one field/method.
	// types.NewSelectionSet should do that for us.

	seen := make(map[*types.Var]bool) // for termination on recursive types

	var visit func(T types.Type)
	visit = func(T types.Type) {
		if T, ok := deref(T).Underlying().(*types.Struct); ok {
			for i := 0; i < T.NumFields(); i++ {
				f := T.Field(i)
				if seen[f] {
					continue
				}
				seen[f] = true
				fields = append(fields, f)
				if f.Anonymous() {
					visit(f.Type())
				}
			}
		}
	}
	visit(T)

	return fields
}

// typeIsValid reports whether typ doesn't contain any Invalid types.
func typeIsValid(typ types.Type) bool {
	switch typ := typ.Underlying().(type) {
	case *types.Basic:
		return typ.Kind() != types.Invalid
	case *types.Array:
		return typeIsValid(typ.Elem())
	case *types.Slice:
		return typeIsValid(typ.Elem())
	case *types.Pointer:
		return typeIsValid(typ.Elem())
	case *types.Map:
		return typeIsValid(typ.Key()) && typeIsValid(typ.Elem())
	case *types.Chan:
		return typeIsValid(typ.Elem())
	case *types.Signature:
		return typeIsValid(typ.Params()) && typeIsValid(typ.Results())
	case *types.Tuple:
		for i := 0; i < typ.Len(); i++ {
			if !typeIsValid(typ.At(i).Type()) {
				return false
			}
		}
		return true
	case *types.Struct, *types.Interface, *types.Named:
		// Don't bother checking structs, interfaces, or named types for validity.
		return true
	default:
		return false
	}
}

// resolveInvalid traverses the node of the AST that defines the scope
// containing the declaration of obj, and attempts to find a user-friendly
// name for its invalid type. The resulting Object and its Type are fake.
func resolveInvalid(fset *token.FileSet, obj types.Object, node ast.Node, info *types.Info) types.Object {
	var resultExpr ast.Expr
	ast.Inspect(node, func(node ast.Node) bool {
		switch n := node.(type) {
		case *ast.ValueSpec:
			for _, name := range n.Names {
				if info.Defs[name] == obj {
					resultExpr = n.Type
				}
			}
			return false
		case *ast.Field: // This case handles parameters and results of a FuncDecl or FuncLit.
			for _, name := range n.Names {
				if info.Defs[name] == obj {
					resultExpr = n.Type
				}
			}
			return false
		default:
			return true
		}
	})
	// Construct a fake type for the object and return a fake object with this type.
	typename := formatNode(fset, resultExpr)
	typ := types.NewNamed(types.NewTypeName(token.NoPos, obj.Pkg(), typename, nil), types.Typ[types.Invalid], nil)
	return types.NewVar(obj.Pos(), obj.Pkg(), obj.Name(), typ)
}

func isPointer(T types.Type) bool {
	_, ok := T.(*types.Pointer)
	return ok
}

// deref returns a pointer's element type, traversing as many levels as needed.
// Otherwise it returns typ.
func deref(typ types.Type) types.Type {
	for {
		p, ok := typ.Underlying().(*types.Pointer)
		if !ok {
			return typ
		}
		typ = p.Elem()
	}
}

func isTypeName(obj types.Object) bool {
	_, ok := obj.(*types.TypeName)
	return ok
}

func isFunc(obj types.Object) bool {
	_, ok := obj.(*types.Func)
	return ok
}

func isEmptyInterface(T types.Type) bool {
	intf, _ := T.(*types.Interface)
	return intf != nil && intf.NumMethods() == 0
}

func isUntyped(T types.Type) bool {
	if basic, ok := T.(*types.Basic); ok {
		return basic.Info()&types.IsUntyped > 0
	}
	return false
}

// isSelector returns the enclosing *ast.SelectorExpr when pos is in the
// selector.
func enclosingSelector(path []ast.Node, pos token.Pos) *ast.SelectorExpr {
	if len(path) == 0 {
		return nil
	}

	if sel, ok := path[0].(*ast.SelectorExpr); ok {
		return sel
	}

	if _, ok := path[0].(*ast.Ident); ok && len(path) > 1 {
		if sel, ok := path[1].(*ast.SelectorExpr); ok && pos >= sel.Sel.Pos() {
			return sel
		}
	}

	return nil
}

// typeConversion returns the type being converted to if call is a type
// conversion expression.
func typeConversion(call *ast.CallExpr, info *types.Info) types.Type {
	var ident *ast.Ident
	switch expr := call.Fun.(type) {
	case *ast.Ident:
		ident = expr
	case *ast.SelectorExpr:
		ident = expr.Sel
	default:
		return nil
	}

	// Type conversion (e.g. "float64(foo)").
	if fun, _ := info.ObjectOf(ident).(*types.TypeName); fun != nil {
		return fun.Type()
	}

	return nil
}

// fieldsAccessible returns whether s has at least one field accessible by p.
func fieldsAccessible(s *types.Struct, p *types.Package) bool {
	for i := 0; i < s.NumFields(); i++ {
		f := s.Field(i)
		if f.Exported() || f.Pkg() == p {
			return true
		}
	}
	return false
}

func formatParams(s Snapshot, pkg Package, sig *types.Signature, qf types.Qualifier) []string {
	params := make([]string, 0, sig.Params().Len())
	for i := 0; i < sig.Params().Len(); i++ {
		el := sig.Params().At(i)
		typ, err := formatFieldType(s, pkg, el, qf)
		if err != nil {
			typ = types.TypeString(el.Type(), qf)
		}

		// Handle a variadic parameter (can only be the final parameter).
		if sig.Variadic() && i == sig.Params().Len()-1 {
			typ = strings.Replace(typ, "[]", "...", 1)
		}

		if el.Name() == "" {
			params = append(params, typ)
		} else {
			params = append(params, el.Name()+" "+typ)
		}
	}
	return params
}

func formatFieldType(s Snapshot, srcpkg Package, obj types.Object, qf types.Qualifier) (string, error) {
	file, pkg, err := s.View().FindPosInPackage(srcpkg, obj.Pos())
	if err != nil {
		return "", err
	}
	ident, err := findIdentifier(s, pkg, file, obj.Pos())
	if err != nil {
		return "", err
	}
	if i := ident.ident; i == nil || i.Obj == nil || i.Obj.Decl == nil {
		return "", errors.Errorf("no object for ident %v", i.Name)
	}
	f, ok := ident.ident.Obj.Decl.(*ast.Field)
	if !ok {
		return "", errors.Errorf("ident %s is not a field type", ident.Name)
	}
	return formatNode(s.View().Session().Cache().FileSet(), f.Type), nil
}

func formatNode(fset *token.FileSet, n ast.Node) string {
	var buf strings.Builder
	if err := printer.Fprint(&buf, fset, n); err != nil {
		return ""
	}
	return buf.String()
}

func formatResults(tup *types.Tuple, qf types.Qualifier) ([]string, bool) {
	var writeResultParens bool
	results := make([]string, 0, tup.Len())
	for i := 0; i < tup.Len(); i++ {
		if i >= 1 {
			writeResultParens = true
		}
		el := tup.At(i)
		typ := types.TypeString(el.Type(), qf)

		if el.Name() == "" {
			results = append(results, typ)
		} else {
			if i == 0 {
				writeResultParens = true
			}
			results = append(results, el.Name()+" "+typ)
		}
	}
	return results, writeResultParens
}

// formatType returns the detail and kind for an object of type *types.TypeName.
func formatType(typ types.Type, qf types.Qualifier) (detail string, kind protocol.CompletionItemKind) {
	if types.IsInterface(typ) {
		detail = "interface{...}"
		kind = protocol.InterfaceCompletion
	} else if _, ok := typ.(*types.Struct); ok {
		detail = "struct{...}"
		kind = protocol.StructCompletion
	} else if typ != typ.Underlying() {
		detail, kind = formatType(typ.Underlying(), qf)
	} else {
		detail = types.TypeString(typ, qf)
		kind = protocol.ClassCompletion
	}
	return detail, kind
}

func formatFunction(params []string, results []string, writeResultParens bool) string {
	var detail strings.Builder

	detail.WriteByte('(')
	for i, p := range params {
		if i > 0 {
			detail.WriteString(", ")
		}
		detail.WriteString(p)
	}
	detail.WriteByte(')')

	// Add space between parameters and results.
	if len(results) > 0 {
		detail.WriteByte(' ')
	}

	if writeResultParens {
		detail.WriteByte('(')
	}
	for i, p := range results {
		if i > 0 {
			detail.WriteString(", ")
		}
		detail.WriteString(p)
	}
	if writeResultParens {
		detail.WriteByte(')')
	}

	return detail.String()
}

func SortDiagnostics(d []Diagnostic) {
	sort.Slice(d, func(i int, j int) bool {
		return CompareDiagnostic(d[i], d[j]) < 0
	})
}

func CompareDiagnostic(a, b Diagnostic) int {
	if r := protocol.CompareRange(a.Range, b.Range); r != 0 {
		return r
	}
	if a.Message < b.Message {
		return -1
	}
	if a.Message == b.Message {
		return 0
	}
	return 1
}
