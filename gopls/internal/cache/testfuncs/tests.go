// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package testfuncs

import (
	"go/ast"
	"go/constant"
	"go/token"
	"go/types"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/tools/gopls/internal/cache/parsego"
	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/gopls/internal/util/frob"
	"golang.org/x/tools/gopls/internal/util/safetoken"
)

// An Index records the test set of a package.
type Index struct {
	pkg gobPackage
}

// Decode decodes the given gob-encoded data as an Index.
func Decode(data []byte) *Index {
	var pkg gobPackage
	packageCodec.Decode(data, &pkg)
	return &Index{pkg}
}

// Encode encodes the receiver as gob-encoded data.
func (index *Index) Encode() []byte {
	return packageCodec.Encode(index.pkg)
}

func (index *Index) All() []Result {
	var results []Result
	for _, file := range index.pkg.Files {
		for _, test := range file.Tests {
			results = append(results, test.result())
		}
	}
	return results
}

// A Result reports a test function
type Result struct {
	Location protocol.Location // location of the test
	Name     string            // name of the test
}

// NewIndex returns a new index of method-set information for all
// package-level types in the specified package.
func NewIndex(files []*parsego.File, info *types.Info) *Index {
	b := &indexBuilder{
		fileIndex: make(map[protocol.DocumentURI]int),
		subNames:  make(map[string]int),
		visited:   make(map[*types.Func]bool),
	}
	return b.build(files, info)
}

// build adds to the index all tests of the specified package.
func (b *indexBuilder) build(files []*parsego.File, info *types.Info) *Index {
	for _, file := range files {
		if !strings.HasSuffix(file.Tok.Name(), "_test.go") {
			continue
		}

		for _, decl := range file.File.Decls {
			decl, ok := decl.(*ast.FuncDecl)
			if !ok {
				continue
			}
			obj, ok := info.ObjectOf(decl.Name).(*types.Func)
			if !ok || !obj.Exported() {
				continue
			}

			// error.Error has empty Position, PkgPath, and ObjectPath.
			if obj.Pkg() == nil {
				continue
			}

			isTest, isExample := isTestOrExample(obj)
			if !isTest && !isExample {
				continue
			}

			var t gobTest
			t.Name = decl.Name.Name
			t.Location.URI = file.URI
			t.Location.Range, _ = file.NodeRange(decl)

			i, ok := b.fileIndex[t.Location.URI]
			if !ok {
				i = len(b.Files)
				b.Files = append(b.Files, gobFile{})
				b.fileIndex[t.Location.URI] = i
			}

			b.Files[i].Tests = append(b.Files[i].Tests, t)
			b.visited[obj] = true

			// Check for subtests
			if isTest {
				b.Files[i].Tests = append(b.Files[i].Tests, b.findSubtests(t, decl.Type, decl.Body, file, files, info)...)
			}
		}
	}

	return &Index{pkg: b.gobPackage}
}

func (b *indexBuilder) findSubtests(parent gobTest, typ *ast.FuncType, body *ast.BlockStmt, file *parsego.File, files []*parsego.File, info *types.Info) []gobTest {
	if body == nil {
		return nil
	}

	// If the [testing.T] parameter is unnamed, the func cannot call
	// [testing.T.Run] and thus cannot create any subtests
	if len(typ.Params.List[0].Names) == 0 {
		return nil
	}

	// This "can't fail" because testKind should guarantee that the function has
	// one parameter and the check above guarantees that parameter is named
	param := info.ObjectOf(typ.Params.List[0].Names[0])

	// Find statements of form t.Run(name, func(...) {...}) where t is the
	// parameter of the enclosing test function.
	var tests []gobTest
	for _, stmt := range body.List {
		// Handle direct t.Run calls
		if expr, ok := stmt.(*ast.ExprStmt); ok {
			tests = append(tests, b.findDirectSubtests(parent, param, expr, file, files, info)...)
			continue
		}

		// Handle table-driven tests: for _, tt := range tests { t.Run(tt.name, ...) }
		if rangeStmt, ok := stmt.(*ast.RangeStmt); ok {
			tests = append(tests, b.findTableDrivenSubtests(parent, param, rangeStmt, file, files, info)...)
			continue
		}
	}
	return tests
}

// findDirectSubtests finds subtests from direct t.Run("name", ...) calls.
func (b *indexBuilder) findDirectSubtests(parent gobTest, param types.Object, expr *ast.ExprStmt, file *parsego.File, files []*parsego.File, info *types.Info) []gobTest {
	var tests []gobTest

	call, ok := expr.X.(*ast.CallExpr)
	if !ok || len(call.Args) != 2 {
		return nil
	}
	fun, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || fun.Sel.Name != "Run" {
		return nil
	}
	recv, ok := fun.X.(*ast.Ident)
	if !ok || info.ObjectOf(recv) != param {
		return nil
	}

	sig, ok := info.TypeOf(call.Args[1]).(*types.Signature)
	if !ok {
		return nil
	}
	if _, ok := testKind(sig); !ok {
		return nil // subtest has wrong signature
	}

	val := info.Types[call.Args[0]].Value // may be zero
	if val == nil || val.Kind() != constant.String {
		return nil
	}

	var t gobTest
	t.Name = b.uniqueName(parent.Name, rewrite(constant.StringVal(val)))
	t.Location.URI = file.URI
	t.Location.Range, _ = file.NodeRange(call)
	tests = append(tests, t)

	fn, funcType, funcBody := findFunc(files, info, nil, call.Args[1])
	if funcType == nil {
		return tests
	}

	// Function literals don't have an associated object
	if fn == nil {
		tests = append(tests, b.findSubtests(t, funcType, funcBody, file, files, info)...)
		return tests
	}

	// Never recurse if the second argument is a top-level test function
	if isTest, _ := isTestOrExample(fn); isTest {
		return tests
	}

	// Don't recurse into functions that have already been visited
	if b.visited[fn] {
		return tests
	}

	b.visited[fn] = true
	tests = append(tests, b.findSubtests(t, funcType, funcBody, file, files, info)...)
	return tests
}

// findTableDrivenSubtests finds subtests from table-driven tests.
// It handles patterns like:
//
//	tests := []struct{ name string; ... }{{name: "test1"}, ...}
//	for _, tt := range tests {
//	    t.Run(tt.name, func(t *testing.T) { ... })
//	}
func (b *indexBuilder) findTableDrivenSubtests(parent gobTest, param types.Object, rangeStmt *ast.RangeStmt, file *parsego.File, files []*parsego.File, info *types.Info) []gobTest {
	var tests []gobTest

	// rangeStmt.Body should contain t.Run calls
	if rangeStmt.Body == nil {
		return nil
	}

	// Get the loop variable (e.g., tt in "for _, tt := range tests")
	var loopVar types.Object
	if rangeStmt.Value != nil {
		if ident, ok := rangeStmt.Value.(*ast.Ident); ok {
			loopVar = info.ObjectOf(ident)
		}
	}
	if loopVar == nil {
		// Try rangeStmt.Key for "for tt := range tests" pattern
		if rangeStmt.Key != nil {
			if ident, ok := rangeStmt.Key.(*ast.Ident); ok {
				loopVar = info.ObjectOf(ident)
			}
		}
	}
	if loopVar == nil {
		return nil
	}

	var testNameField *ast.Ident
	// Find t.Run calls in the range body to confirm this is a table-driven test, if so then set the testNameVar
	hasRun := false
	for _, stmt := range rangeStmt.Body.List {
		expr, ok := stmt.(*ast.ExprStmt)
		if !ok {
			continue
		}

		call, ok := expr.X.(*ast.CallExpr)
		if !ok || len(call.Args) != 2 {
			continue
		}
		fun, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || fun.Sel.Name != "Run" {
			continue
		}
		recv, ok := fun.X.(*ast.Ident)
		if !ok || info.ObjectOf(recv) != param {
			continue
		}

		sig, ok := info.TypeOf(call.Args[1]).(*types.Signature)
		if !ok {
			continue
		}
		if _, ok := testKind(sig); !ok {
			continue // subtest has wrong signature
		}

		// Check if first argument is a field access like tt.name, if so set
		testNameField = b.isLoopVarFieldAccess(call.Args[0], loopVar, info)
		if testNameField == nil {
			continue
		}

		// TODO: handle expressions other than struct field selectors

		hasRun = true
		break
	}

	if !hasRun {
		return nil
	}

	// Find the table being ranged over and extract test cases with their locations
	tableEntries := b.extractTableTestCases(rangeStmt.X, files, info, file, testNameField)
	if len(tableEntries) == 0 {
		return nil
	}

	// Create a test entry for each table entry with its specific location
	for _, entry := range tableEntries {
		var t gobTest
		t.Name = b.uniqueName(parent.Name, rewrite(entry.name))
		t.Location.URI = file.URI
		t.Location.Range = entry.location
		tests = append(tests, t)
	}

	return tests
}

// isLoopVarFieldAccess checks if expr is a field access on the loop variable, if so returns the field identifier
// (e.g., tt.name where tt is the loop variable).
func (b *indexBuilder) isLoopVarFieldAccess(expr ast.Expr, loopVar types.Object, info *types.Info) *ast.Ident {
	sel, ok := expr.(*ast.SelectorExpr)
	if !ok {
		return nil
	}
	ident, ok := sel.X.(*ast.Ident)
	if !ok {
		return nil
	}
	if info.ObjectOf(ident) != loopVar {
		return nil
	}
	return sel.Sel
}

// tableTestCase represents a single test case in a table-driven test
type tableTestCase struct {
	name     string
	location protocol.Range
}

// extractTableTestCases extracts test cases with their locations from a table-driven test slice.
// It handles patterns like:
//   - tests := []struct{name string}{{"test1"}, {"test2"}}
//   - []struct{name string}{{"test1"}, {"test2"}}
//   - For identifier references, attempts to find the composite literal value
func (b *indexBuilder) extractTableTestCases(expr ast.Expr, files []*parsego.File, info *types.Info, file *parsego.File, testNameField *ast.Ident) []tableTestCase {
	// Unwrap parentheses
	for {
		if paren, ok := expr.(*ast.ParenExpr); ok {
			expr = paren.X
		} else {
			break
		}
	}

	// Handle both direct composite literals and identifiers
	var comp *ast.CompositeLit
	switch e := expr.(type) {
	case *ast.CompositeLit:
		comp = e
	case *ast.Ident:
		// Look for the assignment of this identifier
		obj := info.ObjectOf(e)
		if obj == nil {
			return nil
		}
		// Find the composite literal from the identifier's definition
		comp = b.findCompositeLiteralForIdent(e, files, info)
		if comp == nil {
			return nil
		}
	default:
		return nil
	}

	// comp should be a slice composite literal
	if comp.Type == nil {
		return nil
	}

	var cases []tableTestCase
	for _, elt := range comp.Elts {
		// Each element should be a struct literal
		structLit, ok := elt.(*ast.CompositeLit)
		if !ok {
			continue
		}

		if len(structLit.Elts) == 0 {
			continue
		}

		// Try keyed fields first (e.g., {name: "test1", ...})
		for _, field := range structLit.Elts {
			kv, ok := field.(*ast.KeyValueExpr)
			if !ok {
				//TODO: look for unkeyed fields
				continue
			}
			key, ok := kv.Key.(*ast.Ident)
			if !ok || key.Name != testNameField.Name {
				continue
			}

			// Get the location of this test case (the struct literal)
			rng, err := file.NodeRange(structLit)
			if err != nil {
				continue
			}

			// Extract the string value
			if val := info.Types[kv.Value].Value; val != nil && val.Kind() == constant.String {
				cases = append(cases, tableTestCase{
					name:     constant.StringVal(val),
					location: rng,
				})
				break
			}
		}
	}

	return cases
}

// findCompositeLiteralForIdent finds the composite literal that initializes the given identifier.
// It searches through the files for variable declarations and assignments.
func (b *indexBuilder) findCompositeLiteralForIdent(ident *ast.Ident, files []*parsego.File, info *types.Info) *ast.CompositeLit {
	obj := info.ObjectOf(ident)
	if obj == nil {
		return nil
	}

	// Search through all files to find the declaration
	for _, file := range files {
		// Walk through declarations to find variable declarations
		for _, decl := range file.File.Decls {
			// Check function declarations (where local variables are declared)
			funcDecl, ok := decl.(*ast.FuncDecl)
			if !ok || funcDecl.Body == nil {
				continue
			}

			// Walk through statements in the function body
			for _, stmt := range funcDecl.Body.List {
				// Check for short variable declaration: tests := ...
				if assign, ok := stmt.(*ast.AssignStmt); ok && assign.Tok == token.DEFINE {
					for i, lhs := range assign.Lhs {
						if lhsIdent, ok := lhs.(*ast.Ident); ok && info.ObjectOf(lhsIdent) == obj {
							// Found the declaration, check if RHS is a composite literal
							if i < len(assign.Rhs) {
								if comp, ok := assign.Rhs[i].(*ast.CompositeLit); ok {
									return comp
								}
							}
						}
					}
				}

				// Check for var declaration: var tests = ...
				if declStmt, ok := stmt.(*ast.DeclStmt); ok {
					if genDecl, ok := declStmt.Decl.(*ast.GenDecl); ok && genDecl.Tok == token.VAR {
						for _, spec := range genDecl.Specs {
							if valueSpec, ok := spec.(*ast.ValueSpec); ok {
								for i, name := range valueSpec.Names {
									if info.ObjectOf(name) == obj && i < len(valueSpec.Values) {
										if comp, ok := valueSpec.Values[i].(*ast.CompositeLit); ok {
											return comp
										}
									}
								}
							}
						}
					}
				}
			}
		}
	}

	return nil
}

// findFunc finds the type and body of the given expr, which may be a function
// literal or reference to a declared function. If the expression is a declared
// function, findFunc returns its [types.Func]. If the expression is a function
// literal, findFunc returns nil for the first return value. If no function is
// found, findFunc returns (nil, nil, nil).
func findFunc(files []*parsego.File, info *types.Info, body *ast.BlockStmt, expr ast.Expr) (*types.Func, *ast.FuncType, *ast.BlockStmt) {
	var obj types.Object
	switch arg := expr.(type) {
	case *ast.FuncLit:
		return nil, arg.Type, arg.Body

	case *ast.Ident:
		obj = info.ObjectOf(arg)
		if obj == nil {
			return nil, nil, nil
		}

	case *ast.SelectorExpr:
		// Look for methods within the current package. We will not handle
		// imported functions and methods for now, as that would require access
		// to the source of other packages and would be substantially more
		// complex. However, those cases should be rare.
		sel, ok := info.Selections[arg]
		if !ok {
			return nil, nil, nil
		}
		obj = sel.Obj()

	default:
		return nil, nil, nil
	}

	if v, ok := obj.(*types.Var); ok {
		// TODO: Handle vars. This could handled by walking over the body (and
		// the file), but that doesn't account for assignment. If the variable
		// is assigned multiple times, we could easily get the wrong one.
		_, _ = v, body
		return nil, nil, nil
	}

	for _, file := range files {
		// Skip files that don't contain the object (there should only be a
		// single file that _does_ contain it)
		if _, err := safetoken.Offset(file.Tok, obj.Pos()); err != nil {
			continue
		}

		for _, decl := range file.File.Decls {
			decl, ok := decl.(*ast.FuncDecl)
			if !ok {
				continue
			}

			if info.ObjectOf(decl.Name) == obj {
				return obj.(*types.Func), decl.Type, decl.Body
			}
		}
	}
	return nil, nil, nil
}

// isTestOrExample reports whether the given func is a testing func or an
// example func (or neither). isTestOrExample returns (true, false) for testing
// funcs, (false, true) for example funcs, and (false, false) otherwise.
func isTestOrExample(fn *types.Func) (isTest, isExample bool) {
	sig := fn.Type().(*types.Signature)
	if sig.Params().Len() == 0 &&
		sig.Results().Len() == 0 {
		return false, isTestName(fn.Name(), "Example")
	}

	kind, ok := testKind(sig)
	if !ok {
		return false, false
	}
	switch kind.Name() {
	case "T":
		return isTestName(fn.Name(), "Test"), false
	case "B":
		return isTestName(fn.Name(), "Benchmark"), false
	case "F":
		return isTestName(fn.Name(), "Fuzz"), false
	default:
		return false, false // "can't happen" (see testKind)
	}
}

// isTestName reports whether name is a valid test name for the test kind
// indicated by the given prefix ("Test", "Benchmark", etc.).
//
// Adapted from go/analysis/passes/tests.
func isTestName(name, prefix string) bool {
	suffix, ok := strings.CutPrefix(name, prefix)
	if !ok {
		return false
	}
	if len(suffix) == 0 {
		// "Test" is ok.
		return true
	}
	r, _ := utf8.DecodeRuneInString(suffix)
	return !unicode.IsLower(r)
}

// testKind returns the parameter type TypeName of a test, benchmark, or fuzz
// function (one of testing.[TBF]).
func testKind(sig *types.Signature) (*types.TypeName, bool) {
	if sig.Params().Len() != 1 ||
		sig.Results().Len() != 0 {
		return nil, false
	}

	ptr, ok := sig.Params().At(0).Type().(*types.Pointer)
	if !ok {
		return nil, false
	}

	named, ok := ptr.Elem().(*types.Named)
	if !ok || named.Obj().Pkg() == nil || named.Obj().Pkg().Path() != "testing" {
		return nil, false
	}

	switch named.Obj().Name() {
	case "T", "B", "F":
		return named.Obj(), true
	}
	return nil, false
}

// An indexBuilder builds an index for a single package.
type indexBuilder struct {
	gobPackage
	fileIndex map[protocol.DocumentURI]int
	subNames  map[string]int
	visited   map[*types.Func]bool
}

// -- serial format of index --

// (The name says gob but in fact we use frob.)
var packageCodec = frob.CodecFor[gobPackage]()

// A gobPackage records the test set of each package-level type for a single package.
type gobPackage struct {
	Files []gobFile
}

type gobFile struct {
	Tests []gobTest
}

// A gobTest records the name, type, and position of a single test.
type gobTest struct {
	Location protocol.Location // location of the test
	Name     string            // name of the test
}

func (t *gobTest) result() Result {
	return Result(*t)
}
