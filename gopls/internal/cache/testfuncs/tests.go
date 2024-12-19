// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package testfuncs

import (
	"go/ast"
	"go/constant"
	"go/types"
	"regexp"
	"strings"

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

		val := info.Types[call.Args[0]].Value // may be zero
		if val == nil || val.Kind() != constant.String {
			continue
		}

		var t gobTest
		t.Name = b.uniqueName(parent.Name, rewrite(constant.StringVal(val)))
		t.Location.URI = file.URI
		t.Location.Range, _ = file.NodeRange(call)
		tests = append(tests, t)

		if typ, body := findFunc(files, info, body, call.Args[1]); typ != nil {
			tests = append(tests, b.findSubtests(t, typ, body, file, files, info)...)
		}
	}
	return tests
}

// findFunc finds the type and body of the given expr, which may be a function
// literal or reference to a declared function.
//
// If no function is found, findFunc returns (nil, nil).
func findFunc(files []*parsego.File, info *types.Info, body *ast.BlockStmt, expr ast.Expr) (*ast.FuncType, *ast.BlockStmt) {
	var obj types.Object
	switch arg := expr.(type) {
	case *ast.FuncLit:
		return arg.Type, arg.Body

	case *ast.Ident:
		obj = info.ObjectOf(arg)
		if obj == nil {
			return nil, nil
		}

	case *ast.SelectorExpr:
		// Look for methods within the current package. We will not handle
		// imported functions and methods for now, as that would require access
		// to the source of other packages and would be substantially more
		// complex. However, those cases should be rare.
		sel, ok := info.Selections[arg]
		if !ok {
			return nil, nil
		}
		obj = sel.Obj()

	default:
		return nil, nil
	}

	if v, ok := obj.(*types.Var); ok {
		// TODO: Handle vars. This could handled by walking over the body (and
		// the file), but that doesn't account for assignment. If the variable
		// is assigned multiple times, we could easily get the wrong one.
		_, _ = v, body
		return nil, nil
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
				return decl.Type, decl.Body
			}
		}
	}
	return nil, nil
}

var (
	reTest      = regexp.MustCompile(`^Test([A-Z]|$)`)
	reBenchmark = regexp.MustCompile(`^Benchmark([A-Z]|$)`)
	reFuzz      = regexp.MustCompile(`^Fuzz([A-Z]|$)`)
	reExample   = regexp.MustCompile(`^Example([A-Z]|$)`)
)

// isTestOrExample reports whether the given func is a testing func or an
// example func (or neither). isTestOrExample returns (true, false) for testing
// funcs, (false, true) for example funcs, and (false, false) otherwise.
func isTestOrExample(fn *types.Func) (isTest, isExample bool) {
	sig := fn.Type().(*types.Signature)
	if sig.Params().Len() == 0 &&
		sig.Results().Len() == 0 {
		return false, reExample.MatchString(fn.Name())
	}

	kind, ok := testKind(sig)
	if !ok {
		return false, false
	}
	switch kind.Name() {
	case "T":
		return reTest.MatchString(fn.Name()), false
	case "B":
		return reBenchmark.MatchString(fn.Name()), false
	case "F":
		return reFuzz.MatchString(fn.Name()), false
	default:
		return false, false // "can't happen" (see testKind)
	}
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
