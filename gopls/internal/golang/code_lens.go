// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package golang

import (
	"context"
	"go/ast"
	"go/token"
	"go/types"
	"regexp"
	"strings"

	"golang.org/x/tools/gopls/internal/cache"
	"golang.org/x/tools/gopls/internal/cache/parsego"
	"golang.org/x/tools/gopls/internal/file"
	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/gopls/internal/protocol/command"
	"golang.org/x/tools/internal/typesinternal"
)

type LensFunc func(context.Context, *cache.Snapshot, file.Handle) ([]protocol.CodeLens, error)

// LensFuncs returns the supported lensFuncs for Go files.
func LensFuncs() map[command.Command]LensFunc {
	return map[command.Command]LensFunc{
		command.Generate:      goGenerateCodeLens,
		command.Test:          runTestCodeLens,
		command.RegenerateCgo: regenerateCgoLens,
		command.GCDetails:     toggleDetailsCodeLens,
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
	fns, err := TestsAndBenchmarks(pkg, pgf)
	if err != nil {
		return nil, err
	}
	puri := fh.URI()
	for _, fn := range fns.Tests {
		cmd, err := command.NewTestCommand("run test", puri, []string{fn.Name}, nil)
		if err != nil {
			return nil, err
		}
		rng := protocol.Range{Start: fn.Rng.Start, End: fn.Rng.Start}
		codeLens = append(codeLens, protocol.CodeLens{Range: rng, Command: &cmd})
	}

	for _, fn := range fns.Benchmarks {
		cmd, err := command.NewTestCommand("run benchmark", puri, nil, []string{fn.Name})
		if err != nil {
			return nil, err
		}
		rng := protocol.Range{Start: fn.Rng.Start, End: fn.Rng.Start}
		codeLens = append(codeLens, protocol.CodeLens{Range: rng, Command: &cmd})
	}

	if len(fns.Benchmarks) > 0 {
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
		for _, fn := range fns.Benchmarks {
			benches = append(benches, fn.Name)
		}
		cmd, err := command.NewTestCommand("run file benchmarks", puri, nil, benches)
		if err != nil {
			return nil, err
		}
		codeLens = append(codeLens, protocol.CodeLens{Range: rng, Command: &cmd})
	}
	return codeLens, nil
}

type TestFn struct {
	Name string
	Rng  protocol.Range
}

type TestFns struct {
	Tests      []TestFn
	Benchmarks []TestFn
}

func TestsAndBenchmarks(pkg *cache.Package, pgf *parsego.File) (TestFns, error) {
	var out TestFns

	if !strings.HasSuffix(pgf.URI.Path(), "_test.go") {
		return out, nil
	}

	for _, d := range pgf.File.Decls {
		fn, ok := d.(*ast.FuncDecl)
		if !ok {
			continue
		}

		rng, err := pgf.NodeRange(fn)
		if err != nil {
			return out, err
		}

		if matchTestFunc(fn, pkg, testRe, "T") {
			out.Tests = append(out.Tests, TestFn{fn.Name.Name, rng})
		}

		if matchTestFunc(fn, pkg, benchmarkRe, "B") {
			out.Benchmarks = append(out.Benchmarks, TestFn{fn.Name.Name, rng})
		}
	}

	return out, nil
}

func matchTestFunc(fn *ast.FuncDecl, pkg *cache.Package, nameRe *regexp.Regexp, paramID string) bool {
	// Make sure that the function name matches a test function.
	if !nameRe.MatchString(fn.Name.Name) {
		return false
	}
	info := pkg.GetTypesInfo()
	if info == nil {
		return false
	}
	obj := info.ObjectOf(fn.Name)
	if obj == nil {
		return false
	}
	sig, ok := obj.Type().(*types.Signature)
	if !ok {
		return false
	}
	// Test functions should have only one parameter.
	if sig.Params().Len() != 1 {
		return false
	}

	// Check the type of the only parameter
	isptr, named := typesinternal.ReceiverNamed(sig.Params().At(0))
	if !isptr || named == nil {
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
			nonRecursiveCmd, err := command.NewGenerateCommand("run go generate", command.GenerateArgs{Dir: dir, Recursive: false})
			if err != nil {
				return nil, err
			}
			recursiveCmd, err := command.NewGenerateCommand("run go generate ./...", command.GenerateArgs{Dir: dir, Recursive: true})
			if err != nil {
				return nil, err
			}
			return []protocol.CodeLens{
				{Range: rng, Command: &recursiveCmd},
				{Range: rng, Command: &nonRecursiveCmd},
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
	cmd, err := command.NewRegenerateCgoCommand("regenerate cgo definitions", command.URIArg{URI: puri})
	if err != nil {
		return nil, err
	}
	return []protocol.CodeLens{{Range: rng, Command: &cmd}}, nil
}

func toggleDetailsCodeLens(ctx context.Context, snapshot *cache.Snapshot, fh file.Handle) ([]protocol.CodeLens, error) {
	pgf, err := snapshot.ParseGo(ctx, fh, parsego.Full)
	if err != nil {
		return nil, err
	}
	if !pgf.File.Package.IsValid() {
		// Without a package name we have nowhere to put the codelens, so give up.
		return nil, nil
	}
	rng, err := pgf.PosRange(pgf.File.Package, pgf.File.Package)
	if err != nil {
		return nil, err
	}
	puri := fh.URI()
	cmd, err := command.NewGCDetailsCommand("Toggle gc annotation details", puri)
	if err != nil {
		return nil, err
	}
	return []protocol.CodeLens{{Range: rng, Command: &cmd}}, nil
}
