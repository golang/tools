// Copyright 2022 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package checker_test

import (
	"bytes"
	"flag"
	"fmt"
	"go/ast"
	"go/token"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"testing"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/checker"
	"golang.org/x/tools/go/analysis/multichecker"
	"golang.org/x/tools/go/analysis/passes/inspect"
	"golang.org/x/tools/go/ast/inspector"
	"golang.org/x/tools/go/expect"
	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/internal/diff"
	"golang.org/x/tools/internal/testenv"
	"golang.org/x/tools/internal/testfiles"
	"golang.org/x/tools/txtar"
)

func TestMain(m *testing.M) {
	// If the CHECKER_TEST_CHILD environment variable is set,
	// this process should behave like a multichecker.
	// Analyzers are selected by flags.
	if _, ok := os.LookupEnv("CHECKER_TEST_CHILD"); ok {
		multichecker.Main(
			markerAnalyzer,
			noendAnalyzer,
			renameAnalyzer,
		)
		panic("unreachable")
	}

	// ordinary test
	flag.Parse()
	os.Exit(m.Run())
}

const (
	exitCodeSuccess     = 0 // success (no diagnostics, or successful -fix)
	exitCodeFailed      = 1 // analysis failed to run
	exitCodeDiagnostics = 3 // diagnostics were reported (and no -fix)
)

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
			`analyzer "a" suggests invalid fix .*: no token.File for TextEdit.Pos .0.`,
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
			`analyzer "a" suggests invalid fix .*: TextEdit.Pos .* > TextEdit.End .*`,
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

// TestScript runs script-driven tests in testdata/*.txt.
// Each file is a txtar archive, expanded to a temporary directory.
//
// The comment section of the archive is a script, with the following
// commands:
//
//	# comment
//		ignored
//	blank line
//		ignored
//	skip k=v...
//		Skip the test if any k=v string is a substring of the string
//		"GOOS=darwin GOARCH=arm64" appropriate to the current build.
//	checker args...
//		Run the checker command with the specified space-separated
//		arguments; this fork+execs the [TestMain] function above.
//		If the archive has a "stdout" section, its contents must
//		match the stdout output of the checker command.
//		Do NOT use this for testing -diff: tests should not
//		rely on the particulars of the diff algorithm.
//	exit int
//		Assert that previous checker command had this exit code.
//	stderr regexp
//		Assert that stderr output from previous checker run matches this pattern.
//
// The script must include at least one 'checker' command.
func TestScript(t *testing.T) {
	testenv.NeedsExec(t)
	testenv.NeedsGoPackages(t)

	txtfiles, err := filepath.Glob("testdata/*.txt")
	if err != nil {
		t.Fatal(err)
	}
	for _, txtfile := range txtfiles {
		t.Run(txtfile, func(t *testing.T) {
			t.Parallel()

			// Expand archive into tmp tree.
			ar, err := txtar.ParseFile(txtfile)
			if err != nil {
				t.Fatal(err)
			}
			fs, err := txtar.FS(ar)
			if err != nil {
				t.Fatal(err)
			}
			dir := testfiles.CopyToTmp(t, fs)

			// Parse txtar comment as a script.
			const noExitCode = -999
			var (
				// state variables operated on by script
				lastExitCode = noExitCode
				lastStderr   string
			)
			for i, line := range strings.Split(string(ar.Comment), "\n") {
				line = strings.TrimSpace(line)
				if line == "" || line[0] == '#' {
					continue // skip blanks and comments
				}

				command, rest, _ := strings.Cut(line, " ")
				prefix := fmt.Sprintf("%s:%d: %s", txtfile, i+1, command) // for error messages
				switch command {
				case "checker":
					cmd := exec.Command(os.Args[0], strings.Fields(rest)...)
					cmd.Dir = dir
					cmd.Stdout = new(strings.Builder)
					cmd.Stderr = new(strings.Builder)
					cmd.Env = append(os.Environ(), "CHECKER_TEST_CHILD=1", "GOPROXY=off")
					if err := cmd.Run(); err != nil {
						if err, ok := err.(*exec.ExitError); ok {
							lastExitCode = err.ExitCode()
							// fall through
						} else {
							t.Fatalf("%s: failed to execute checker: %v (%s)", prefix, err, cmd)
						}
					} else {
						lastExitCode = 0 // success
					}

					// Eliminate nondeterministic strings from the output.
					clean := func(x any) string {
						s := fmt.Sprint(x)
						pwd, _ := os.Getwd()
						if realDir, err := filepath.EvalSymlinks(dir); err == nil {
							// Work around checker's packages.Load failing to
							// set Config.Dir to dir, causing the filenames
							// of loaded packages not to be a subdir of dir.
							s = strings.ReplaceAll(s, realDir, dir)
						}
						s = strings.ReplaceAll(s, dir, string(os.PathSeparator)+"TMP")
						s = strings.ReplaceAll(s, pwd, string(os.PathSeparator)+"PWD")
						s = strings.ReplaceAll(s, cmd.Path, filepath.Base(cmd.Path))
						return s
					}

					lastStderr = clean(cmd.Stderr)
					stdout := clean(cmd.Stdout)

					// Detect bad markers out of band:
					// though they cause a non-zero exit,
					// that may be expected.
					if strings.Contains(lastStderr, badMarker) {
						t.Errorf("marker analyzer encountered errors; stderr=%s", lastStderr)
					}

					// debugging
					if false {
						t.Logf("%s: $ %s\nstdout:\n%s\nstderr:\n%s", prefix, clean(cmd), stdout, lastStderr)
					}

					// Keep error reporting logic below consistent with
					// applyDiffsAndCompare in ../../analysistest/analysistest.go!

					unified := func(xlabel, ylabel string, x, y []byte) string {
						x = append(slices.Clip(bytes.TrimSpace(x)), '\n')
						y = append(slices.Clip(bytes.TrimSpace(y)), '\n')
						return diff.Unified(xlabel, ylabel, string(x), string(y))
					}

					// Check stdout, if there's a section of that name.
					//
					// Do not use this for testing -diff! It exposes tests to the
					// internals of our (often suboptimal) diff algorithm.
					// Instead, use the want/ mechanism.
					if f := section(ar, "stdout"); f != nil {
						got, want := []byte(stdout), f.Data
						if diff := unified("got", "want", got, want); diff != "" {
							t.Errorf("%s: unexpected stdout: -- got --\n%s-- want --\n%s-- diff --\n%s",
								prefix,
								got, want, diff)
						}
					}

					for _, f := range ar.Files {
						// For each file named want/X, assert that the
						// current content of X now equals want/X.
						if filename, ok := strings.CutPrefix(f.Name, "want/"); ok {
							fixed, err := os.ReadFile(filepath.Join(dir, filename))
							if err != nil {
								t.Errorf("reading %s: %v", filename, err)
								continue
							}
							var original []byte
							if f := section(ar, filename); f != nil {
								original = f.Data
							}
							want := f.Data
							if diff := unified(filename+" (fixed)", filename+" (want)", fixed, want); diff != "" {
								t.Errorf("%s: unexpected %s content:\n"+
									"-- original --\n%s\n"+
									"-- fixed --\n%s\n"+
									"-- want --\n%s\n"+
									"-- diff original fixed --\n%s\n"+
									"-- diff fixed want --\n%s",
									prefix, filename,
									original,
									fixed,
									want,
									unified(filename+" (original)", filename+" (fixed)", original, fixed),
									diff)
							}
						}
					}

				case "skip":
					config := fmt.Sprintf("GOOS=%s GOARCH=%s", runtime.GOOS, runtime.GOARCH)
					for _, word := range strings.Fields(rest) {
						if strings.Contains(config, word) {
							t.Skip(word)
						}
					}

				case "exit":
					if lastExitCode == noExitCode {
						t.Fatalf("%s: no prior 'checker' command", prefix)
					}
					var want int
					if _, err := fmt.Sscanf(rest, "%d", &want); err != nil {
						t.Fatalf("%s: requires one numeric operand", prefix)
					}
					if want != lastExitCode {
						// plan9 ExitCode() currently only returns 0 for success or 1 for failure
						if !(runtime.GOOS == "plan9" && want != exitCodeSuccess && lastExitCode != exitCodeSuccess) {
							t.Errorf("%s: exit code was %d, want %d", prefix, lastExitCode, want)
						}
					}

				case "stderr":
					if lastExitCode == noExitCode {
						t.Fatalf("%s: no prior 'checker' command", prefix)
					}
					if matched, err := regexp.MatchString(rest, lastStderr); err != nil {
						t.Fatalf("%s: invalid regexp: %v", prefix, err)
					} else if !matched {
						t.Errorf("%s: output didn't match pattern %q:\n%s", prefix, rest, lastStderr)
					}

				default:
					t.Errorf("%s: unknown command", prefix)
				}
			}
			if lastExitCode == noExitCode {
				t.Errorf("test script contains no 'checker' command")
			}
		})
	}
}

const badMarker = "[bad marker]"

// The marker analyzer generates fixes from @marker annotations in the
// source. Each marker is of the form:
//
//	@message("pattern", "replacement)
//
// The "message" is used for both the Diagnostic.Message and
// SuggestedFix.Message field. Multiple markers with the same
// message form a single diagnostic and fix with a list of textedits.
//
// The "pattern" is a regular expression that must match on the
// current line (though it may extend beyond if the pattern starts
// with "(?s)"), and whose extent forms the TextEdit.{Pos,End}
// deletion. If the pattern contains one subgroup, its range will be
// used; this allows contextual matching.
//
// The "replacement" is a literal string that forms the
// TextEdit.NewText.
//
// Fixes are applied in the order they are first mentioned in the
// source.
var markerAnalyzer = &analysis.Analyzer{
	Name:     "marker",
	Doc:      "doc",
	Requires: []*analysis.Analyzer{inspect.Analyzer},
	Run: func(pass *analysis.Pass) (_ any, err error) {
		// Errors returned by this analyzer cause the
		// checker command to exit non-zero, but that
		// may be the expected outcome for other reasons
		// (e.g. there were diagnostics).
		//
		// So, we report these errors out of band by logging
		// them with a special badMarker string that the
		// TestScript harness looks for, to ensure that the
		// test fails in that case.
		defer func() {
			if err != nil {
				log.Printf("%s: %v", badMarker, err)
			}
		}()

		// Parse all notes in the files.
		var keys []string
		edits := make(map[string][]analysis.TextEdit)
		for _, file := range pass.Files {
			tokFile := pass.Fset.File(file.FileStart)
			content, err := pass.ReadFile(tokFile.Name())
			if err != nil {
				return nil, err
			}
			notes, err := expect.ExtractGo(pass.Fset, file)
			if err != nil {
				return nil, err
			}
			for _, note := range notes {
				edit, err := markerEdit(tokFile, content, note)
				if err != nil {
					return nil, fmt.Errorf("%s: %v", tokFile.Position(note.Pos), err)
				}
				// Preserve note order as it determines fix order.
				if edits[note.Name] == nil {
					keys = append(keys, note.Name)
				}
				edits[note.Name] = append(edits[note.Name], edit)
			}
		}

		// Report each fix in its own Diagnostic.
		for _, key := range keys {
			edits := edits[key]
			// debugging
			if false {
				log.Printf("%s: marker: @%s: %+v", pass.Fset.Position(edits[0].Pos), key, edits)
			}
			pass.Report(analysis.Diagnostic{
				Pos:     edits[0].Pos,
				End:     edits[0].Pos,
				Message: key,
				SuggestedFixes: []analysis.SuggestedFix{{
					Message:   key,
					TextEdits: edits,
				}},
			})
		}
		return nil, nil
	},
}

// markerEdit returns the TextEdit denoted by note.
func markerEdit(tokFile *token.File, content []byte, note *expect.Note) (analysis.TextEdit, error) {
	if len(note.Args) != 2 {
		return analysis.TextEdit{}, fmt.Errorf("got %d args, want @%s(pattern, replacement)", len(note.Args), note.Name)
	}

	pattern, ok := note.Args[0].(string)
	if !ok {
		return analysis.TextEdit{}, fmt.Errorf("got %T for pattern, want string", note.Args[0])
	}
	rx, err := regexp.Compile(pattern)
	if err != nil {
		return analysis.TextEdit{}, fmt.Errorf("invalid pattern regexp: %v", err)
	}

	// Match the pattern against the current line.
	lineStart := tokFile.LineStart(tokFile.Position(note.Pos).Line)
	lineStartOff := tokFile.Offset(lineStart)
	lineEndOff := tokFile.Offset(note.Pos)
	matches := rx.FindSubmatchIndex(content[lineStartOff:])
	if len(matches) == 0 {
		return analysis.TextEdit{}, fmt.Errorf("no match for regexp %q", rx)
	}
	var start, end int // line-relative offset
	switch len(matches) {
	case 2:
		// no subgroups: return the range of the regexp expression
		start, end = matches[0], matches[1]
	case 4:
		// one subgroup: return its range
		start, end = matches[2], matches[3]
	default:
		return analysis.TextEdit{}, fmt.Errorf("invalid location regexp %q: expect either 0 or 1 subgroups, got %d", rx, len(matches)/2-1)
	}
	if start > lineEndOff-lineStartOff {
		// The start of the match must be between the start of the line and the
		// marker position (inclusive).
		return analysis.TextEdit{}, fmt.Errorf("no matching range found starting on the current line")
	}

	replacement, ok := note.Args[1].(string)
	if !ok {
		return analysis.TextEdit{}, fmt.Errorf("second argument must be pattern, got %T", note.Args[1])
	}

	// debugging: show matched portion
	if false {
		log.Printf("%s: %s: r%q (%q) -> %q",
			tokFile.Position(note.Pos),
			note.Name,
			pattern,
			content[lineStartOff+start:lineStartOff+end],
			replacement)
	}

	return analysis.TextEdit{
		Pos:     lineStart + token.Pos(start),
		End:     lineStart + token.Pos(end),
		NewText: []byte(replacement),
	}, nil
}

var renameAnalyzer = &analysis.Analyzer{
	Name:             "rename",
	Requires:         []*analysis.Analyzer{inspect.Analyzer},
	Doc:              "renames symbols named bar to baz",
	RunDespiteErrors: true,
	Run: func(pass *analysis.Pass) (any, error) {
		const (
			from = "bar"
			to   = "baz"
		)
		inspect := pass.ResultOf[inspect.Analyzer].(*inspector.Inspector)
		nodeFilter := []ast.Node{(*ast.Ident)(nil)}
		inspect.Preorder(nodeFilter, func(n ast.Node) {
			ident := n.(*ast.Ident)
			if ident.Name == from {
				msg := fmt.Sprintf("renaming %q to %q", from, to)
				pass.Report(analysis.Diagnostic{
					Pos:     ident.Pos(),
					End:     ident.End(),
					Message: msg,
					SuggestedFixes: []analysis.SuggestedFix{{
						Message: msg,
						TextEdits: []analysis.TextEdit{{
							Pos:     ident.Pos(),
							End:     ident.End(),
							NewText: []byte(to),
						}},
					}},
				})
			}
		})
		return nil, nil
	},
}

var noendAnalyzer = &analysis.Analyzer{
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
				TextEdits: []analysis.TextEdit{{
					Pos:     decl.Pos(),
					End:     token.NoPos,
					NewText: []byte("/*hello*/"),
				}},
			}},
		})
		return nil, nil
	},
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

// section returns the named archive section, or nil.
func section(ar *txtar.Archive, name string) *txtar.File {
	for i, f := range ar.Files {
		if f.Name == name {
			return &ar.Files[i]
		}
	}
	return nil
}
