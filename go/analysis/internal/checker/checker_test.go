// Copyright 2019 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package checker_test

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/analysistest"
	"golang.org/x/tools/go/analysis/internal/checker"
	"golang.org/x/tools/go/analysis/passes/inspect"
	"golang.org/x/tools/internal/testenv"
	"golang.org/x/tools/internal/testfiles"
	"golang.org/x/tools/txtar"
)

func TestApplyFixes(t *testing.T) {
	testenv.NeedsGoPackages(t)
	testenv.RedirectStderr(t) // associated checker.Run output with this test

	files := map[string]string{
		"rename/test.go": `package rename

func Foo() {
	bar := 12
	_ = bar
}

// the end
`}
	want := `package rename

func Foo() {
	baz := 12
	_ = baz
}

// the end
`

	testdata, cleanup, err := analysistest.WriteFiles(files)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(testdata, "src/rename/test.go")

	checker.Fix = true
	checker.Run([]string{"file=" + path}, []*analysis.Analyzer{renameAnalyzer})
	checker.Fix = false

	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	got := string(contents)
	if got != want {
		t.Errorf("contents of rewritten file\ngot: %s\nwant: %s", got, want)
	}

	defer cleanup()
}

func TestRunDespiteErrors(t *testing.T) {
	testenv.NeedsGoPackages(t)
	testenv.RedirectStderr(t) // associate checker.Run output with this test

	files := map[string]string{
		"rderr/test.go": `package rderr

// Foo deliberately has a type error
func Foo(s string) int {
	return s + 1
}
`,
		"cperr/test.go": `package copyerr

import "sync"

func bar() { } // for renameAnalyzer

type T struct{ mu sync.Mutex }
type T1 struct{ t *T }

func NewT1() *T1 { return &T1{T} }
`,
	}

	testdata, cleanup, err := analysistest.WriteFiles(files)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()

	rderrFile := "file=" + filepath.Join(testdata, "src/rderr/test.go")
	cperrFile := "file=" + filepath.Join(testdata, "src/cperr/test.go")

	// A no-op analyzer that should finish regardless of
	// parse or type errors in the code.
	noop := &analysis.Analyzer{
		Name:     "noop",
		Doc:      "noop",
		Requires: []*analysis.Analyzer{inspect.Analyzer},
		Run: func(pass *analysis.Pass) (any, error) {
			return nil, nil
		},
		RunDespiteErrors: true,
	}

	// A no-op analyzer, with facts, that should finish
	// regardless of parse or type errors in the code.
	noopWithFact := &analysis.Analyzer{
		Name:     "noopfact",
		Doc:      "noopfact",
		Requires: []*analysis.Analyzer{inspect.Analyzer},
		Run: func(pass *analysis.Pass) (any, error) {
			return nil, nil
		},
		RunDespiteErrors: true,
		FactTypes:        []analysis.Fact{&EmptyFact{}},
	}

	for _, test := range []struct {
		name      string
		pattern   []string
		analyzers []*analysis.Analyzer
		code      int
	}{
		// parse/type errors
		{name: "skip-error", pattern: []string{rderrFile}, analyzers: []*analysis.Analyzer{renameAnalyzer}, code: 1},
		// RunDespiteErrors allows a driver to run an Analyzer even after parse/type errors.
		//
		// The noop analyzer doesn't use facts, so the driver loads only the root
		// package from source. For the rest, it asks 'go list' for export data,
		// which fails because the compiler encounters the type error.  Since the
		// errors come from 'go list', the driver doesn't run the analyzer.
		{name: "despite-error", pattern: []string{rderrFile}, analyzers: []*analysis.Analyzer{noop}, code: exitCodeFailed},
		// The noopfact analyzer does use facts, so the driver loads source for
		// all dependencies, does type checking itself, recognizes the error as a
		// type error, and runs the analyzer.
		{name: "despite-error-fact", pattern: []string{rderrFile}, analyzers: []*analysis.Analyzer{noopWithFact}, code: exitCodeFailed},
		// combination of parse/type errors and no errors
		{name: "despite-error-and-no-error", pattern: []string{rderrFile, "sort"}, analyzers: []*analysis.Analyzer{renameAnalyzer, noop}, code: exitCodeFailed},
		// non-existing package error
		{name: "no-package", pattern: []string{"xyz"}, analyzers: []*analysis.Analyzer{renameAnalyzer}, code: exitCodeFailed},
		{name: "no-package-despite-error", pattern: []string{"abc"}, analyzers: []*analysis.Analyzer{noop}, code: exitCodeFailed},
		{name: "no-multi-package-despite-error", pattern: []string{"xyz", "abc"}, analyzers: []*analysis.Analyzer{noop}, code: exitCodeFailed},
		// combination of type/parsing and different errors
		{name: "different-errors", pattern: []string{rderrFile, "xyz"}, analyzers: []*analysis.Analyzer{renameAnalyzer, noop}, code: exitCodeFailed},
		// non existing dir error
		{name: "no-match-dir", pattern: []string{"file=non/existing/dir"}, analyzers: []*analysis.Analyzer{renameAnalyzer, noop}, code: exitCodeFailed},
		// no errors
		{name: "no-errors", pattern: []string{"sort"}, analyzers: []*analysis.Analyzer{renameAnalyzer, noop}, code: exitCodeSuccess},
		// duplicate list error with no findings
		{name: "list-error", pattern: []string{cperrFile}, analyzers: []*analysis.Analyzer{noop}, code: exitCodeFailed},
		// duplicate list errors with findings (issue #67790)
		{name: "list-error-findings", pattern: []string{cperrFile}, analyzers: []*analysis.Analyzer{renameAnalyzer}, code: exitCodeDiagnostics},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := checker.Run(test.pattern, test.analyzers); got != test.code {
				t.Errorf("got incorrect exit code %d for test %s; want %d", got, test.name, test.code)
			}
		})
	}
}

type EmptyFact struct{}

func (f *EmptyFact) AFact() {}

func TestURL(t *testing.T) {
	// TestURL test that URLs get forwarded to diagnostics by internal/checker.
	testenv.NeedsGoPackages(t)

	files := map[string]string{
		"p/test.go": `package p // want "package name is p"`,
	}
	pkgname := &analysis.Analyzer{
		Name: "pkgname",
		Doc:  "trivial analyzer that reports package names",
		URL:  "https://pkg.go.dev/golang.org/x/tools/go/analysis/internal/checker",
		Run: func(p *analysis.Pass) (any, error) {
			for _, f := range p.Files {
				p.ReportRangef(f.Name, "package name is %s", f.Name.Name)
			}
			return nil, nil
		},
	}

	testdata, cleanup, err := analysistest.WriteFiles(files)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	path := filepath.Join(testdata, "src/p/test.go")
	results := analysistest.Run(t, testdata, pkgname, "file="+path)

	var urls []string
	for _, r := range results {
		for _, d := range r.Diagnostics {
			urls = append(urls, d.URL)
		}
	}
	want := []string{"https://pkg.go.dev/golang.org/x/tools/go/analysis/internal/checker"}
	if !reflect.DeepEqual(urls, want) {
		t.Errorf("Expected Diagnostics.URLs %v. got %v", want, urls)
	}
}

// TestPassReadFile exercises the Pass.ReadFile function.
func TestPassReadFile(t *testing.T) {
	cwd, _ := os.Getwd()

	const src = `
-- go.mod --
module example.com

-- p/file.go --
package p

-- p/ignored.go --
//go:build darwin && mips64

package p

hello from ignored

-- p/other.s --
hello from other
`

	// Expand archive into tmp tree.
	fs, err := txtar.FS(txtar.Parse([]byte(src)))
	if err != nil {
		t.Fatal(err)
	}
	tmpdir := testfiles.CopyToTmp(t, fs)

	ran := false
	a := &analysis.Analyzer{
		Name:     "a",
		Requires: []*analysis.Analyzer{inspect.Analyzer},
		Doc:      "doc",
		Run: func(pass *analysis.Pass) (any, error) {
			if len(pass.OtherFiles)+len(pass.IgnoredFiles) == 0 {
				t.Errorf("OtherFiles and IgnoredFiles are empty")
				return nil, nil
			}

			for _, test := range []struct {
				filename string
				want     string // substring of file content or error message
			}{
				{
					pass.OtherFiles[0], // [other.s]
					"hello from other",
				},
				{
					pass.IgnoredFiles[0], // [ignored.go]
					"hello from ignored",
				},
				{
					"nonesuch",
					"nonesuch is not among OtherFiles, ", // etc
				},
				{
					filepath.Join(cwd, "checker_test.go"),
					"checker_test.go is not among OtherFiles, ", // etc
				},
			} {
				content, err := pass.ReadFile(test.filename)
				var got string
				if err != nil {
					got = err.Error()
				} else {
					got = string(content)
					if len(got) > 100 {
						got = got[:100] + "..."
					}
				}
				if !strings.Contains(got, test.want) {
					t.Errorf("Pass.ReadFile(%q) did not contain %q; got:\n%s",
						test.filename, test.want, got)
				}
			}
			ran = true
			return nil, nil
		},
	}

	analysistest.Run(t, tmpdir, a, "example.com/p")

	if !ran {
		t.Error("analyzer did not run")
	}

	// TODO(adonovan): test that fixes are applied to the
	// pass.ReadFile virtual file tree.
}
