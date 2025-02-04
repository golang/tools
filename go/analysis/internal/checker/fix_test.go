// Copyright 2022 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package checker_test

import (
	"flag"
	"fmt"
	"go/token"
	"log"
	"os"
	"os/exec"
	"path"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/analysistest"
	"golang.org/x/tools/go/analysis/checker"
	"golang.org/x/tools/go/analysis/multichecker"
	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/internal/testenv"
)

// These are the analyzers available to the multichecker.
// (Tests may add more in init functions as needed.)
var candidates = map[string]*analysis.Analyzer{
	renameAnalyzer.Name: renameAnalyzer,
	otherAnalyzer.Name:  otherAnalyzer,
}

func TestMain(m *testing.M) {
	// If the ANALYZERS=a,..,z environment is set, then this
	// process should behave like a multichecker with the
	// named analyzers.
	if s, ok := os.LookupEnv("ANALYZERS"); ok {
		var analyzers []*analysis.Analyzer
		for _, name := range strings.Split(s, ",") {
			a := candidates[name]
			if a == nil {
				log.Fatalf("no such analyzer: %q", name)
			}
			analyzers = append(analyzers, a)
		}
		multichecker.Main(analyzers...)
		panic("unreachable")
	}

	// ordinary test
	flag.Parse()
	os.Exit(m.Run())
}

const (
	exitCodeSuccess     = 0 // success (no diagnostics)
	exitCodeFailed      = 1 // analysis failed to run
	exitCodeDiagnostics = 3 // diagnostics were reported
)

// fix runs a multichecker subprocess with -fix in the specified
// directory, applying the comma-separated list of named analyzers to
// the packages matching the patterns. It returns the CombinedOutput.
func fix(t *testing.T, dir, analyzers string, wantExit int, patterns ...string) string {
	testenv.NeedsExec(t)
	testenv.NeedsTool(t, "go")

	cmd := exec.Command(os.Args[0], "-fix")
	cmd.Args = append(cmd.Args, patterns...)
	cmd.Env = append(os.Environ(),
		"ANALYZERS="+analyzers,
		"GOPATH="+dir,
		"GO111MODULE=off",
		"GOPROXY=off")

	clean := func(s string) string {
		return strings.ReplaceAll(s, os.TempDir(), "os.TempDir/")
	}
	outBytes, err := cmd.CombinedOutput()
	switch err := err.(type) {
	case nil:
		// success
	case *exec.ExitError:
		if code := err.ExitCode(); code != wantExit {
			// plan9 ExitCode() currently only returns 0 for success or 1 for failure
			if !(runtime.GOOS == "plan9" && wantExit != exitCodeSuccess && code != exitCodeSuccess) {
				t.Errorf("exit code was %d, want %d", code, wantExit)
			}
		}
	default:
		t.Fatalf("failed to execute multichecker: %v", err)
	}

	out := clean(string(outBytes))
	t.Logf("$ %s\n%s", clean(fmt.Sprint(cmd)), out)

	return out
}

// TestFixes ensures that checker.Run applies fixes correctly.
// This test fork/execs the main function above.
func TestFixes(t *testing.T) {
	files := map[string]string{
		"rename/foo.go": `package rename

func Foo() {
	bar := 12
	_ = bar
}

// the end
`,
		"rename/intestfile_test.go": `package rename

func InTestFile() {
	bar := 13
	_ = bar
}

// the end
`,
		"rename/foo_test.go": `package rename_test

func Foo() {
	bar := 14
	_ = bar
}

// the end
`,
	}
	fixed := map[string]string{
		"rename/foo.go": `package rename

func Foo() {
	baz := 12
	_ = baz
}

// the end
`,
		"rename/intestfile_test.go": `package rename

func InTestFile() {
	baz := 13
	_ = baz
}

// the end
`,
		"rename/foo_test.go": `package rename_test

func Foo() {
	baz := 14
	_ = baz
}

// the end
`,
	}
	dir, cleanup, err := analysistest.WriteFiles(files)
	if err != nil {
		t.Fatalf("Creating test files failed with %s", err)
	}
	defer cleanup()

	fix(t, dir, "rename,other", exitCodeDiagnostics, "rename")

	for name, want := range fixed {
		path := path.Join(dir, "src", name)
		contents, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("error reading %s: %v", path, err)
		}
		if got := string(contents); got != want {
			t.Errorf("contents of %s file did not match expectations. got=%s, want=%s", path, got, want)
		}
	}
}

// TestReportInvalidDiagnostic tests that a call to pass.Report with
// certain kind of invalid diagnostic (e.g. conflicting fixes)
// promptly results in a panic.
func TestReportInvalidDiagnostic(t *testing.T) {
	testenv.NeedsGoPackages(t)

	// Load the errors package.
	cfg := &packages.Config{Mode: packages.LoadAllSyntax}
	initial, err := packages.Load(cfg, "errors")
	if err != nil {
		t.Fatal(err)
	}

	for _, test := range []struct {
		name string
		want string
		diag func(pos token.Pos) analysis.Diagnostic
	}{
		// Diagnostic has two alternative fixes with the same Message.
		{
			"duplicate message",
			`analyzer "a" suggests two fixes with same Message \(fix\)`,
			func(pos token.Pos) analysis.Diagnostic {
				return analysis.Diagnostic{
					Pos:     pos,
					Message: "oops",
					SuggestedFixes: []analysis.SuggestedFix{
						{Message: "fix"},
						{Message: "fix"},
					},
				}
			},
		},
		// TextEdit has invalid Pos.
		{
			"bad Pos",
			`analyzer "a" suggests invalid fix .*: missing file info for pos`,
			func(pos token.Pos) analysis.Diagnostic {
				return analysis.Diagnostic{
					Pos:     pos,
					Message: "oops",
					SuggestedFixes: []analysis.SuggestedFix{
						{
							Message:   "fix",
							TextEdits: []analysis.TextEdit{{}},
						},
					},
				}
			},
		},
		// TextEdit has invalid End.
		{
			"End < Pos",
			`analyzer "a" suggests invalid fix .*: pos .* > end`,
			func(pos token.Pos) analysis.Diagnostic {
				return analysis.Diagnostic{
					Pos:     pos,
					Message: "oops",
					SuggestedFixes: []analysis.SuggestedFix{
						{
							Message: "fix",
							TextEdits: []analysis.TextEdit{{
								Pos: pos + 2,
								End: pos,
							}},
						},
					},
				}
			},
		},
		// Two TextEdits overlap.
		{
			"overlapping edits",
			`analyzer "a" suggests invalid fix .*: overlapping edits to .*errors.go \(1:1-1:3 and 1:2-1:4\)`,
			func(pos token.Pos) analysis.Diagnostic {
				return analysis.Diagnostic{
					Pos:     pos,
					Message: "oops",
					SuggestedFixes: []analysis.SuggestedFix{
						{
							Message: "fix",
							TextEdits: []analysis.TextEdit{
								{Pos: pos, End: pos + 2},
								{Pos: pos + 1, End: pos + 3},
							},
						},
					},
				}
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			reached := false
			a := &analysis.Analyzer{Name: "a", Doc: "doc", Run: func(pass *analysis.Pass) (any, error) {
				reached = true
				panics(t, test.want, func() {
					pos := pass.Files[0].FileStart
					pass.Report(test.diag(pos))
				})
				return nil, nil
			}}
			if _, err := checker.Analyze([]*analysis.Analyzer{a}, initial, &checker.Options{}); err != nil {
				t.Fatalf("Analyze failed: %v", err)
			}
			if !reached {
				t.Error("analyzer was never invoked")
			}
		})
	}
}

// TestConflict ensures that checker.Run detects conflicts correctly.
// This test fork/execs the main function above.
func TestConflict(t *testing.T) {
	files := map[string]string{
		"conflict/foo.go": `package conflict

func Foo() {
	bar := 12
	_ = bar
}

// the end
`,
	}
	dir, cleanup, err := analysistest.WriteFiles(files)
	if err != nil {
		t.Fatalf("Creating test files failed with %s", err)
	}
	defer cleanup()

	out := fix(t, dir, "rename,other", exitCodeFailed, "conflict")

	pattern := `conflicting edits from rename and rename on .*foo.go`
	matched, err := regexp.MatchString(pattern, out)
	if err != nil {
		t.Errorf("error matching pattern %s: %v", pattern, err)
	} else if !matched {
		t.Errorf("output did not match pattern: %s", pattern)
	}

	// No files updated
	for name, want := range files {
		path := path.Join(dir, "src", name)
		contents, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("error reading %s: %v", path, err)
		}
		if got := string(contents); got != want {
			t.Errorf("contents of %s file updated. got=%s, want=%s", path, got, want)
		}
	}
}

// TestOther ensures that checker.Run reports conflicts from
// distinct actions correctly.
// This test fork/execs the main function above.
func TestOther(t *testing.T) {
	files := map[string]string{
		"other/foo.go": `package other

func Foo() {
	bar := 12
	_ = bar
}

// the end
`,
	}
	dir, cleanup, err := analysistest.WriteFiles(files)
	if err != nil {
		t.Fatalf("Creating test files failed with %s", err)
	}
	defer cleanup()

	// The 'rename' and 'other' analyzers suggest conflicting fixes.
	out := fix(t, dir, "rename,other", exitCodeFailed, "other")

	pattern := `.*conflicting edits from other and rename on .*foo.go`
	matched, err := regexp.MatchString(pattern, out)
	if err != nil {
		t.Errorf("error matching pattern %s: %v", pattern, err)
	} else if !matched {
		t.Errorf("output did not match pattern: %s", pattern)
	}

	// No files updated
	for name, want := range files {
		path := path.Join(dir, "src", name)
		contents, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("error reading %s: %v", path, err)
		}
		if got := string(contents); got != want {
			t.Errorf("contents of %s file updated. got=%s, want=%s", path, got, want)
		}
	}
}

// TestNoEnd tests that a missing SuggestedFix.End position is
// correctly interpreted as if equal to SuggestedFix.Pos (see issue #64199).
func TestNoEnd(t *testing.T) {
	files := map[string]string{
		"a/a.go": "package a\n\nfunc F() {}",
	}
	dir, cleanup, err := analysistest.WriteFiles(files)
	if err != nil {
		t.Fatalf("Creating test files failed with %s", err)
	}
	defer cleanup()

	fix(t, dir, "noend", exitCodeDiagnostics, "a")

	got, err := os.ReadFile(path.Join(dir, "src/a/a.go"))
	if err != nil {
		t.Fatal(err)
	}
	const want = "package a\n\n/*hello*/\nfunc F() {}\n"
	if string(got) != want {
		t.Errorf("new file contents were <<%s>>, want <<%s>>", got, want)
	}
}

func init() {
	candidates["noend"] = &analysis.Analyzer{
		Name: "noend",
		Doc:  "inserts /*hello*/ before first decl",
		Run: func(pass *analysis.Pass) (any, error) {
			decl := pass.Files[0].Decls[0]
			pass.Report(analysis.Diagnostic{
				Pos:     decl.Pos(),
				End:     token.NoPos,
				Message: "say hello",
				SuggestedFixes: []analysis.SuggestedFix{{
					Message: "say hello",
					TextEdits: []analysis.TextEdit{
						{
							Pos:     decl.Pos(),
							End:     token.NoPos,
							NewText: []byte("/*hello*/"),
						},
					},
				}},
			})
			return nil, nil
		},
	}
}

// panics asserts that f() panics with with a value whose printed form matches the regexp want.
func panics(t *testing.T, want string, f func()) {
	defer func() {
		if x := recover(); x == nil {
			t.Errorf("function returned normally, wanted panic")
		} else if m, err := regexp.MatchString(want, fmt.Sprint(x)); err != nil {
			t.Errorf("panics: invalid regexp %q", want)
		} else if !m {
			t.Errorf("function panicked with value %q, want match for %q", x, want)
		}
	}()
	f()
}
