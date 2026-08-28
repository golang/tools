// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package unitchecker_test

// This file illustrates separate analysis with an example.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"testing"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/passes/printf"
	"golang.org/x/tools/go/analysis/unitchecker"
	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/internal/testenv"
	"golang.org/x/tools/internal/testfiles"
	"golang.org/x/tools/txtar"
)

// TestExampleSeparateAnalysis demonstrates the principle of separate
// analysis, the distribution of units of type-checking and analysis
// work across several processes, using serialized summaries to
// communicate between them.
//
// It uses two different kinds of task, "manager" and "worker":
//
//   - The manager computes the graph of package dependencies, and makes
//     a request to the worker for each package. It does not parse,
//     type-check, or analyze Go code. It is analogous "go vet".
//
//   - The worker, which contains the Analyzers, reads each request,
//     loads, parses, and type-checks the files of one package,
//     applies all necessary analyzers to the package, then writes
//     its results to a file. It is a unitchecker-based driver,
//     analogous to the program specified by go vet -vettool= flag.
//
// In practice these would be separate executables, but for simplicity
// of this example they are provided by one executable in two
// different modes: the Example function is the manager, and the same
// executable invoked with ENTRYPOINT=worker is the worker.
// (See TestIntegration for how this happens.)
//
// Unfortunately this can't be a true Example because of the skip,
// which requires a testing.T.
func TestExampleSeparateAnalysis(t *testing.T) {
	testenv.NeedsGoPackages(t)

	// src is an archive containing a module with a printf mistake.
	const src = `
-- go.mod --
module separate
go 1.18

-- main/main.go --
package main

import "separate/lib"

func main() {
	lib.MyPrintf("%s", 123)
}

-- lib/lib.go --
package lib

import "fmt"

func MyPrintf(format string, args ...any) {
	fmt.Printf(format, args...)
}
`

	// Expand archive into tmp tree.
	fs, err := txtar.FS(txtar.Parse([]byte(src)))
	if err != nil {
		t.Fatal(err)
	}
	tmpdir := testfiles.CopyToTmp(t, fs)

	// Load metadata for the main package and all its dependencies.
	cfg := &packages.Config{
		Mode: packages.NeedName | packages.NeedFiles | packages.NeedCompiledGoFiles | packages.NeedImports | packages.NeedModule,
		Dir:  tmpdir,
		Env: append(os.Environ(),
			"GOPROXY=off", // disable network
			"GOWORK=off",  // an ambient GOWORK value would break package loading
		),
		Logf: t.Logf,
	}
	pkgs, err := packages.Load(cfg, "separate/main")
	if err != nil {
		t.Fatal(err)
	}
	// Stop if any package had a metadata error.
	if packages.PrintErrors(pkgs) > 0 {
		t.Fatal("there were errors among loaded packages")
	}

	// Now we have loaded the import graph,
	// let's begin the proper work of the manager.

	// Gather root packages. They will get all analyzers,
	// whereas dependencies get only the subset that
	// produce facts or are required by them.
	roots := make(map[*packages.Package]bool)
	for _, pkg := range pkgs {
		roots[pkg] = true
	}

	// nextID generates sequence numbers for each unit of work.
	// We use it to create names of temporary files.
	var nextID atomic.Int32

	var allDiagnostics []string

	// Visit all packages in postorder: dependencies first.
	// TODO(adonovan): opt: use parallel postorder.
	packages.Visit(pkgs, nil, func(pkg *packages.Package) {
		if pkg.PkgPath == "unsafe" {
			return
		}

		// Choose a unique prefix for temporary files
		// (.cfg .vetx) produced by this package.
		// We stow it in an otherwise unused field of
		// Package so it can be accessed by our importers.
		prefix := fmt.Sprintf("%s/%d", tmpdir, nextID.Add(1))
		pkg.ExportFile = prefix

		// Construct the request to the worker.
		var (
			importMap   = make(map[string]string)
			packageVetx = make(map[string]string)
		)
		for importPath, dep := range pkg.Imports {
			importMap[importPath] = dep.PkgPath
			if depPrefix := dep.ExportFile; depPrefix != "" { // skip "unsafe"
				packageVetx[dep.PkgPath] = depPrefix + ".vetx"
			}
		}
		cfg := unitchecker.Config{
			ID:           pkg.ID,
			ImportPath:   pkg.PkgPath,
			GoFiles:      pkg.CompiledGoFiles,
			NonGoFiles:   pkg.OtherFiles,
			IgnoredFiles: pkg.IgnoredFiles,
			ImportMap:    importMap,
			PackageVetx:  packageVetx,
			VetxOnly:     !roots[pkg],
			VetxOutput:   prefix + ".vetx",
		}
		if pkg.Module != nil {
			if v := pkg.Module.GoVersion; v != "" {
				cfg.GoVersion = "go" + v
			}
			cfg.ModulePath = pkg.Module.Path
			cfg.ModuleVersion = pkg.Module.Version
			cfg.Module = analysisModuleFromPackagesModule(pkg.Module)
		}

		// Write the JSON configuration message to a file.
		cfgData, err := json.Marshal(cfg)
		if err != nil {
			t.Fatalf("internal error in json.Marshal: %v", err)
		}
		cfgFile := prefix + ".cfg"
		if err := os.WriteFile(cfgFile, cfgData, 0666); err != nil {
			t.Fatal(err)
		}

		// Send the request to the worker.
		cmd := testenv.Command(t, os.Args[0], "-json", cfgFile)
		cmd.Stderr = os.Stderr
		cmd.Stdout = new(bytes.Buffer)
		cmd.Env = append(os.Environ(), "ENTRYPOINT=worker")
		if err := cmd.Run(); err != nil {
			t.Fatal(err)
		}

		// Parse JSON output and gather in allDiagnostics.
		dec := json.NewDecoder(cmd.Stdout.(io.Reader))
		for {
			type jsonDiagnostic struct {
				Posn    string `json:"posn"`
				Message string `json:"message"`
			}
			// 'results' maps Package.Path -> Analyzer.Name -> diagnostics
			var results map[string]map[string][]jsonDiagnostic
			if err := dec.Decode(&results); err != nil {
				if err == io.EOF {
					break
				}
				t.Fatalf("internal error decoding JSON: %v", err)
			}
			for _, result := range results {
				for analyzer, diags := range result {
					for _, diag := range diags {
						rel := strings.ReplaceAll(diag.Posn, tmpdir, "")
						rel = filepath.ToSlash(rel)
						msg := fmt.Sprintf("%s: [%s] %s", rel, analyzer, diag.Message)
						allDiagnostics = append(allDiagnostics, msg)
					}
				}
			}
		}
	})

	// Observe that the example produces a fact-based diagnostic
	// from separate analysis of "main", "lib", and "fmt":

	const want = `/main/main.go:6:16: [printf] separate/lib.MyPrintf format %s has arg 123 of wrong type int`
	sort.Strings(allDiagnostics)
	if got := strings.Join(allDiagnostics, "\n"); got != want {
		t.Errorf("Got: %s\nWant: %s", got, want)
	}
}

// -- worker process --

// worker is the main entry point for a unitchecker-based driver
// with only a single analyzer, for illustration.
func worker() {
	unitchecker.Main(printf.Analyzer)
}

// -- helpers --

func analysisModuleFromPackagesModule(mod *packages.Module) *analysis.Module {
	if mod == nil {
		return nil
	}
	var modErr *analysis.ModuleError
	if mod.Error != nil {
		modErr = &analysis.ModuleError{Err: mod.Error.Err}
	}
	return &analysis.Module{
		Path:      mod.Path,
		Version:   mod.Version,
		Replace:   analysisModuleFromPackagesModule(mod.Replace),
		Time:      mod.Time,
		Main:      mod.Main,
		Indirect:  mod.Indirect,
		Dir:       mod.Dir,
		GoMod:     mod.GoMod,
		GoVersion: mod.GoVersion,
		Error:     modErr,
	}
}
