// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package golang

import (
	"cmp"
	"context"
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"regexp"
	"slices"
	"strings"
	"unicode"

	"golang.org/x/tools/gopls/internal/cache"
	"golang.org/x/tools/gopls/internal/cache/metadata"
	"golang.org/x/tools/gopls/internal/cache/parsego"
	"golang.org/x/tools/gopls/internal/cache/testfuncs"
	"golang.org/x/tools/gopls/internal/file"
	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/gopls/internal/protocol/command"
	"golang.org/x/tools/gopls/internal/settings"
	"golang.org/x/tools/internal/astutil"
)

// CodeLensSources returns the supported sources of code lenses for Go files.
func CodeLensSources() map[settings.CodeLensSource]cache.CodeLensSourceFunc {
	return map[settings.CodeLensSource]cache.CodeLensSourceFunc{
		settings.CodeLensGenerate:      goGenerateCodeLens, // commands: Generate
		settings.CodeLensTest:          runTestCodeLens,    // commands: Test
		settings.CodeLensRegenerateCgo: regenerateCgoLens,  // commands: RegenerateCgo
		settings.CodeLensGoToTest:      goToTestCodeLens,   // commands: GoToTest
	}
}

var (
	testRe      = regexp.MustCompile(`^Test([^a-z]|$)`) // TestFoo or Test but not Testable
	benchmarkRe = regexp.MustCompile(`^Benchmark([^a-z]|$)`)
)

func runTestCodeLens(ctx context.Context, snapshot *cache.Snapshot, fh file.Handle) ([]protocol.CodeLens, error) {
	var codeLens []protocol.CodeLens

	pkg, pgf, err := NarrowestPackageForFile(ctx, snapshot, fh.URI())
	if err != nil {
		return nil, err
	}
	testFuncs, benchFuncs, err := testsAndBenchmarks(pkg.TypesInfo(), pgf)
	if err != nil {
		return nil, err
	}
	puri := fh.URI()
	for _, fn := range testFuncs {
		cmd := command.NewRunTestsCommand("run test", command.RunTestsArgs{
			URI:   puri,
			Tests: []string{fn.name},
		})
		rng := protocol.Range{Start: fn.rng.Start, End: fn.rng.Start}
		codeLens = append(codeLens, protocol.CodeLens{Range: rng, Command: cmd})
	}

	for _, fn := range benchFuncs {
		cmd := command.NewRunTestsCommand("run benchmark", command.RunTestsArgs{
			URI:        puri,
			Benchmarks: []string{fn.name},
		})
		rng := protocol.Range{Start: fn.rng.Start, End: fn.rng.Start}
		codeLens = append(codeLens, protocol.CodeLens{Range: rng, Command: cmd})
	}

	if len(benchFuncs) > 0 {
		pgf, err := snapshot.ParseGo(ctx, fh, parsego.Full)
		if err != nil {
			return nil, err
		}
		// add a code lens to the top of the file which runs all benchmarks in the file
		rng, err := pgf.PosRange(pgf.File.Package, pgf.File.Package)
		if err != nil {
			return nil, err
		}
		var benches []string
		for _, fn := range benchFuncs {
			benches = append(benches, fn.name)
		}
		cmd := command.NewRunTestsCommand("run file benchmarks", command.RunTestsArgs{
			URI:        puri,
			Benchmarks: benches,
		})
		codeLens = append(codeLens, protocol.CodeLens{Range: rng, Command: cmd})
	}
	return codeLens, nil
}

type testFunc struct {
	name string
	rng  protocol.Range // of *ast.FuncDecl
}

// testsAndBenchmarks returns all Test and Benchmark functions in the
// specified file.
func testsAndBenchmarks(info *types.Info, pgf *parsego.File) (tests, benchmarks []testFunc, _ error) {
	if !strings.HasSuffix(pgf.URI.Path(), "_test.go") {
		return nil, nil, nil // empty
	}

	for _, d := range pgf.File.Decls {
		fn, ok := d.(*ast.FuncDecl)
		if !ok {
			continue
		}

		rng, err := pgf.NodeRange(fn)
		if err != nil {
			return nil, nil, err
		}

		if matchTestFunc(fn, info, testRe, "T") {
			tests = append(tests, testFunc{fn.Name.Name, rng})
		} else if matchTestFunc(fn, info, benchmarkRe, "B") {
			benchmarks = append(benchmarks, testFunc{fn.Name.Name, rng})
		}
	}
	return
}

func matchTestFunc(fn *ast.FuncDecl, info *types.Info, nameRe *regexp.Regexp, paramID string) bool {
	// Make sure that the function name matches a test function.
	if !nameRe.MatchString(fn.Name.Name) {
		return false
	}
	obj, ok := info.ObjectOf(fn.Name).(*types.Func)
	if !ok {
		return false
	}
	sig := obj.Signature()
	// Test functions should have only one parameter.
	if sig.Params().Len() != 1 {
		return false
	}

	// Check the type of the only parameter
	// (We don't Unalias or use typesinternal.ReceiverNamed
	// in the two checks below because "go test" can't see
	// through aliases when enumerating Test* functions;
	// it's syntactic.)
	paramTyp, ok := sig.Params().At(0).Type().(*types.Pointer)
	if !ok {
		return false
	}
	named, ok := paramTyp.Elem().(*types.Named)
	if !ok {
		return false
	}
	namedObj := named.Obj()
	if namedObj.Pkg().Path() != "testing" {
		return false
	}
	return namedObj.Id() == paramID
}

func goGenerateCodeLens(ctx context.Context, snapshot *cache.Snapshot, fh file.Handle) ([]protocol.CodeLens, error) {
	pgf, err := snapshot.ParseGo(ctx, fh, parsego.Full)
	if err != nil {
		return nil, err
	}
	const ggDirective = "//go:generate"
	for _, c := range pgf.File.Comments {
		for _, l := range c.List {
			if !strings.HasPrefix(l.Text, ggDirective) {
				continue
			}
			rng, err := pgf.PosRange(l.Pos(), l.Pos()+token.Pos(len(ggDirective)))
			if err != nil {
				return nil, err
			}
			dir := fh.URI().Dir()
			nonRecursiveCmd := command.NewGenerateCommand("run go generate", command.GenerateArgs{Dir: dir, Recursive: false})
			recursiveCmd := command.NewGenerateCommand("run go generate ./...", command.GenerateArgs{Dir: dir, Recursive: true})
			return []protocol.CodeLens{
				{Range: rng, Command: recursiveCmd},
				{Range: rng, Command: nonRecursiveCmd},
			}, nil

		}
	}
	return nil, nil
}

func regenerateCgoLens(ctx context.Context, snapshot *cache.Snapshot, fh file.Handle) ([]protocol.CodeLens, error) {
	pgf, err := snapshot.ParseGo(ctx, fh, parsego.Full)
	if err != nil {
		return nil, err
	}
	var c *ast.ImportSpec
	for _, imp := range pgf.File.Imports {
		if imp.Path.Value == `"C"` {
			c = imp
		}
	}
	if c == nil {
		return nil, nil
	}
	rng, err := pgf.NodeRange(c)
	if err != nil {
		return nil, err
	}
	puri := fh.URI()
	cmd := command.NewRegenerateCgoCommand("regenerate cgo definitions", command.URIArg{URI: puri})
	return []protocol.CodeLens{{Range: rng, Command: cmd}}, nil
}

func goToTestCodeLens(ctx context.Context, snapshot *cache.Snapshot, fh file.Handle) ([]protocol.CodeLens, error) {
	if !snapshot.Options().ClientOptions.ShowDocumentSupported {
		// GoToTest command uses 'window/showDocument' request. Don't generate
		// code lenses for clients that won't be able to use them.
		return nil, nil
	}

	matches, err := matchFunctionsWithTests(ctx, snapshot, fh)
	if err != nil {
		return nil, err
	}

	lenses := make([]protocol.CodeLens, 0, len(matches))
	for _, t := range matches {
		lenses = append(lenses, protocol.CodeLens{
			Range:   protocol.Range{Start: t.FuncPos, End: t.FuncPos},
			Command: command.NewGoToTestCommand("Go to "+t.Name, t.Loc),
		})
	}
	return lenses, nil
}

type TestMatch struct {
	FuncPos protocol.Position  // function position
	Name    string             // test name
	Loc     protocol.Location  // test location
	Type    testfuncs.TestType // test type
}

func matchFunctionsWithTests(ctx context.Context, snapshot *cache.Snapshot, fh file.Handle) (matches []TestMatch, err error) {
	if strings.HasSuffix(fh.URI().Path(), "_test.go") {
		// Ignore test files.
		return nil, nil
	}

	// Inspect all packages to cover both "p [p.test]" and "p_test [p.test]".
	allPackages, err := snapshot.WorkspaceMetadata(ctx)
	if err != nil {
		return nil, fmt.Errorf("couldn't request workspace metadata: %w", err)
	}
	dir := fh.URI().Dir()
	testPackages := slices.DeleteFunc(allPackages, func(meta *metadata.Package) bool {
		if meta.IsIntermediateTestVariant() || len(meta.CompiledGoFiles) == 0 || meta.ForTest == "" {
			return true
		}
		return meta.CompiledGoFiles[0].Dir() != dir
	})
	if len(testPackages) == 0 {
		return nil, nil
	}

	pgf, err := snapshot.ParseGo(ctx, fh, parsego.Full)
	if err != nil {
		return nil, fmt.Errorf("couldn't parse file: %w", err)
	}

	type Func struct {
		Name string
		Pos  protocol.Position
	}
	var fileFuncs []Func
	for _, d := range pgf.File.Decls {
		fn, ok := d.(*ast.FuncDecl)
		if !ok {
			continue
		}
		rng, err := pgf.NodeRange(fn)
		if err != nil {
			return nil, fmt.Errorf("couldn't get node range: %w", err)
		}

		name := fn.Name.Name
		if fn.Recv != nil && len(fn.Recv.List) > 0 {
			_, rname, _ := astutil.UnpackRecv(fn.Recv.List[0].Type)
			name = rname.Name + "_" + fn.Name.Name
		}
		fileFuncs = append(fileFuncs, Func{
			Name: name,
			Pos:  rng.Start,
		})
	}

	pkgIDs := make([]PackageID, 0, len(testPackages))
	for _, pkg := range testPackages {
		pkgIDs = append(pkgIDs, pkg.ID)
	}
	allTests, err := snapshot.Tests(ctx, pkgIDs...)
	if err != nil {
		return nil, fmt.Errorf("couldn't request all tests for packages %v: %w", pkgIDs, err)
	}
	for _, tests := range allTests {
		for test := range tests.All() {
			if test.Subtest {
				continue
			}
			potentialFuncNames := getPotentialFuncNames(test)
			if len(potentialFuncNames) == 0 {
				continue
			}

			var matchedFunc Func
			for _, fn := range fileFuncs {
				var matched bool
				for _, potentialName := range potentialFuncNames {
					// Check the prefix to match 'TestDeletePanics' with 'Delete'.
					if strings.HasPrefix(potentialName, fn.Name) {
						matched = true
						break
					}
				}
				if !matched {
					continue
				}

				// Use the most specific function:
				//
				//   - match 'TestDelete', 'TestDeletePanics' with 'Delete'
				//   - match 'TestDeleteFunc', 'TestDeleteFuncClearTail' with 'DeleteFunc', not 'Delete'
				if len(matchedFunc.Name) < len(fn.Name) {
					matchedFunc = fn
				}
			}
			if matchedFunc.Name != "" {
				loc := test.Location
				loc.Range.End = loc.Range.Start // move cursor to the test's beginning

				matches = append(matches, TestMatch{
					FuncPos: matchedFunc.Pos,
					Name:    test.Name,
					Loc:     loc,
					Type:    test.Type,
				})
			}
		}
	}
	if len(matches) == 0 {
		return nil, nil
	}

	slices.SortFunc(matches, func(a, b TestMatch) int {
		if v := protocol.ComparePosition(a.FuncPos, b.FuncPos); v != 0 {
			return v
		}
		if v := cmp.Compare(a.Type, b.Type); v != 0 {
			return v
		}
		return cmp.Compare(a.Name, b.Name)
	})
	return matches, nil
}

func getPotentialFuncNames(test testfuncs.Result) []string {
	var name string
	switch test.Type {
	case testfuncs.TypeTest:
		name = strings.TrimPrefix(test.Name, "Test")
	case testfuncs.TypeBenchmark:
		name = strings.TrimPrefix(test.Name, "Benchmark")
	case testfuncs.TypeFuzz:
		name = strings.TrimPrefix(test.Name, "Fuzz")
	case testfuncs.TypeExample:
		name = strings.TrimPrefix(test.Name, "Example")
	}
	if name == "" {
		return nil
	}
	name = strings.TrimPrefix(name, "_") // 'Foo' for 'TestFoo', 'foo' for 'Test_foo'

	names := []string{name}
	if token.IsExported(name) {
		unexportedName := []rune(name)
		unexportedName[0] = unicode.ToLower(unexportedName[0])
		names = append(names, string(unexportedName)) // 'foo' for 'TestFoo'
	}
	return names
}
