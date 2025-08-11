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
	funcPos := make(map[string]protocol.Position)
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
		funcPos[name] = rng.Start
	}

	type TestType int

	// Types are sorted by priority from high to low.
	const (
		T TestType = iota + 1
		E
		B
		F
	)
	testTypes := map[string]TestType{
		"Test":      T,
		"Example":   E,
		"Benchmark": B,
		"Fuzz":      F,
	}

	type Test struct {
		FuncPos protocol.Position
		Name    string
		Loc     protocol.Location
		Type    TestType
	}
	var matchedTests []Test

	pkgIDs := make([]PackageID, 0, len(testPackages))
	for _, pkg := range testPackages {
		pkgIDs = append(pkgIDs, pkg.ID)
	}
	allTests, err := snapshot.Tests(ctx, pkgIDs...)
	if err != nil {
		return nil, fmt.Errorf("couldn't request all tests for packages %v: %w", pkgIDs, err)
	}
	for _, tests := range allTests {
		for _, test := range tests.All() {
			var (
				name     string
				testType TestType
			)
			for prefix, t := range testTypes {
				if strings.HasPrefix(test.Name, prefix) {
					testType = t
					name = test.Name[len(prefix):]
					break
				}
			}
			if testType == 0 {
				continue // unknown type
			}
			name = strings.TrimPrefix(name, "_")

			// Try to find 'Foo' for 'TestFoo' and 'foo' for 'Test_foo'.
			pos, ok := funcPos[name]
			if !ok && token.IsExported(name) {
				// Try to find 'foo' for 'TestFoo'.
				runes := []rune(name)
				runes[0] = unicode.ToLower(runes[0])
				pos, ok = funcPos[string(runes)]
			}
			if ok {
				loc := test.Location
				loc.Range.End = loc.Range.Start // move cursor to the test's beginning

				matchedTests = append(matchedTests, Test{
					FuncPos: pos,
					Name:    test.Name,
					Loc:     loc,
					Type:    testType,
				})
			}
		}
	}
	if len(matchedTests) == 0 {
		return nil, nil
	}

	slices.SortFunc(matchedTests, func(a, b Test) int {
		if v := protocol.ComparePosition(a.FuncPos, b.FuncPos); v != 0 {
			return v
		}
		if v := cmp.Compare(a.Type, b.Type); v != 0 {
			return v
		}
		return cmp.Compare(a.Name, b.Name)
	})

	lenses := make([]protocol.CodeLens, 0, len(matchedTests))
	for _, t := range matchedTests {
		lenses = append(lenses, protocol.CodeLens{
			Range:   protocol.Range{Start: t.FuncPos, End: t.FuncPos},
			Command: command.NewGoToTestCommand("Go to "+t.Name, t.Loc),
		})
	}
	return lenses, nil
}
