// Copyright 2022 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package bench

import (
	"fmt"
	"go/parser"
	"go/token"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/tools/gopls/internal/protocol"
	. "golang.org/x/tools/gopls/internal/test/integration"
	"golang.org/x/tools/gopls/internal/test/integration/fake"
)

// Use a global edit counter as bench function may execute multiple times, and
// we want to avoid cache hits. Use time.Now to also avoid cache hits from the
// shared file cache.
var editID int64 = time.Now().UnixNano()

type changeTest struct {
	repo    string // repo identifier + optional disambiguating ".foo" suffix
	file    string
	canSave bool
}

var didChangeTests = []changeTest{
	{"google-cloud-go", "internal/annotate.go", true},
	{"istio", "pkg/fuzz/util.go", true},
	{"kubernetes", "pkg/controller/lookup_cache.go", true},
	// disable kubernetes.types as it fails on the builders, but not locally on mac or glinux
	// error is didchange_test.go:134: formatting before save: editing "..."": column is beyond end of line;
	{"kubernetes.types", "staging/src/k8s.io/api/core/v1/types.go", false}, // results in 25K file batch!
	{"kuma", "api/generic/insights.go", true},
	{"oracle", "dataintegration/data_type.go", false}, // diagnoseSave fails because this package is generated
	{"pkgsite", "internal/frontend/server.go", true},
	{"starlark", "starlark/eval.go", true},
	{"tools", "internal/lsp/cache/snapshot.go", true},
}

// BenchmarkDidChange benchmarks modifications of a single file by making
// synthetic modifications in a comment. It controls pacing by waiting for the
// server to actually start processing the didChange notification before
// proceeding. Notably it does not wait for diagnostics to complete.
func BenchmarkDidChange(b *testing.B) {
	for _, test := range didChangeTests {
		b.Run(test.repo, func(b *testing.B) {
			env := getRepo(b, test.repo).sharedEnv(b)
			env.OpenFile(test.file)
			defer closeBuffer(b, env, test.file)

			// Insert the text we'll be modifying at the top of the file.
			edit := makeEditFunc(env, test.file)

			if stopAndRecord := startProfileIfSupported(b, env, qualifiedName(test.repo, "didchange")); stopAndRecord != nil {
				defer stopAndRecord()
			}
			b.ResetTimer()

			for b.Loop() {
				edit()
				env.Await(env.StartedChange())
			}
		})
	}
}

// makeEditFunc prepares the given file for incremental editing, by inserting a
// placeholder comment that will be overwritten with a new unique value by each
// call to the resulting function. While makeEditFunc awaits gopls to finish
// processing the initial edit, the callback for incremental edits does not
// await any gopls state.
//
// This is used for benchmarks that must repeatedly invalidate a file's
// contents.
//
// TODO(rfindley): use this throughout.
func makeEditFunc(env *Env, file string) func() {
	// Insert the text we'll be modifying at the top of the file.
	env.EditBuffer(file, protocol.TextEdit{NewText: "// __TEST_PLACEHOLDER_0__\n"})
	env.AfterChange()

	return func() {
		edits := atomic.AddInt64(&editID, 1)
		env.EditBuffer(file, protocol.TextEdit{
			Range: protocol.Range{
				Start: protocol.Position{Line: 0, Character: 0},
				End:   protocol.Position{Line: 1, Character: 0},
			},
			// Increment the placeholder text, to ensure cache misses.
			NewText: fmt.Sprintf("// __TEST_PLACEHOLDER_%d__\n", edits),
		})
	}
}

func BenchmarkDiagnoseChange(b *testing.B) {
	for _, test := range didChangeTests {
		runChangeDiagnosticsBenchmark(b, test, false, "diagnoseChange")
	}
}

func BenchmarkDiagnoseSave(b *testing.B) {
	for _, test := range didChangeTests {
		runChangeDiagnosticsBenchmark(b, test, true, "diagnoseSave")
	}
}

// runChangeDiagnosticsBenchmark runs a benchmark to edit the test file and
// await the resulting diagnostics pass. If save is set, the file is also saved.
func runChangeDiagnosticsBenchmark(b *testing.B, test changeTest, save bool, operation string) {
	b.Run(test.repo, func(b *testing.B) {
		if !test.canSave {
			b.Skipf("skipping as %s cannot be saved", test.file)
		}
		runDiagnosticsBenchmark(b, test, operation, func(env *Env) func() {
			// Insert the text we'll be modifying at the top of the file.
			env.EditBuffer(test.file, protocol.TextEdit{NewText: "// __TEST_PLACEHOLDER_0__\n"})
			if save {
				env.SaveBuffer(test.file)
			}
			env.AfterChange()
			return func() {
				edits := atomic.AddInt64(&editID, 1)
				env.EditBuffer(test.file, protocol.TextEdit{
					Range: protocol.Range{
						Start: protocol.Position{Line: 0, Character: 0},
						End:   protocol.Position{Line: 1, Character: 0},
					},
					// Increment the placeholder text, to ensure cache misses.
					NewText: fmt.Sprintf("// __TEST_PLACEHOLDER_%d__\n", edits),
				})
				if save {
					env.SaveBuffer(test.file)
				}
			}
		})
	})
}

// runDiagnosticsBenchmark opens test.file in a fresh environment and runs a
// subtest that repeatedly applies an edit and awaits the resulting diagnostics.
// makeEdit prepares the file for incremental editing and returns the function to
// run for each iteration.
func runDiagnosticsBenchmark(b *testing.B, test changeTest, operation string, makeEdit func(*Env) func()) {
	repo := getRepo(b, test.repo)
	config := fake.EditorConfig{
		Env: map[string]string{
			"GOPATH": repo.sharedEnv(b).Sandbox.GOPATH(),
		},
		Settings: map[string]any{
			"diagnosticsDelay": "0s",
		},
	}
	// Use a new env to avoid the diagnostic delay: we want to measure how
	// long it takes to produce the diagnostics.
	env, close := repo.newEnv(b, config, operation, false)
	defer close()
	env.OpenFile(test.file)
	edit := makeEdit(env)
	b.ResetTimer()

	// We must use an extra subtest layer here, so that we only set up the
	// shared env once (otherwise we pay additional overhead and the profiling
	// flags don't work).
	b.Run("diagnose", func(b *testing.B) {
		if stopAndRecord := startProfileIfSupported(b, env, qualifiedName(test.repo, operation)); stopAndRecord != nil {
			defer stopAndRecord()
		}
		for b.Loop() {
			edit()
			env.AfterChange()
		}
	})
}

// BenchmarkImportChange measures an unsaved import-path edit and the resulting
// metadata reload and diagnostics, excluding the diagnostics delay. The edits
// toggle a standard-library import that the file does not otherwise use, so each
// edit changes the set of import paths, both by adding and by removing one; as
// Go reports the mean time per iteration, the two edits are averaged. They vary
// file contents to avoid type-checking result cache hits. This does not measure
// formatting, saving, or downloading modules.
func BenchmarkImportChange(b *testing.B) {
	for _, test := range didChangeTests {
		b.Run(test.repo, func(b *testing.B) {
			// Unlike the diagnose benchmarks, no canSave check is needed here:
			// the import edit is not saved.
			runDiagnosticsBenchmark(b, test, "importChange", func(env *Env) func() {
				return makeImportEditFunc(env, test.file)
			})
		})
	}
}

// makeImportEditFunc inserts a blank import after the package clause, and
// returns a function that toggles that import on and off, alternating between
// two otherwise unused standard-library paths. Toggling covers both adding and
// removing an import path. Each edit also changes a comment on the import's line
// to avoid hitting content-addressed caches for type-checking results. Both
// changes use a single didChange. The edited line always exists, so the edit
// range never needs to be recomputed.
func makeImportEditFunc(env *Env, file string) func() {
	text := env.BufferText(file)
	fset := token.NewFileSet()
	header, err := parser.ParseFile(fset, file, text, parser.ImportsOnly)
	if err != nil {
		env.TB.Fatal(err)
	}
	used := make(map[string]bool)
	for _, imp := range header.Imports {
		path, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			env.TB.Fatal(err)
		}
		used[path] = true
	}
	var paths []string
	for _, path := range []string{"math", "math/cmplx", "math/bits", "container/list", "container/heap"} {
		if !used[path] {
			paths = append(paths, path)
			if len(paths) == 2 {
				break
			}
		}
	}
	if len(paths) != 2 {
		env.TB.Fatalf("no unused import paths for %s", file)
	}

	// Insert after the physical package-clause line, including any trailing
	// comment or semicolon. Preserve the original text after that line.
	offset := fset.File(header.Name.Pos()).Offset(header.Name.End())
	prefix := ""
	if newline := strings.IndexByte(text[offset:], '\n'); newline >= 0 {
		offset += newline + 1
	} else {
		offset = len(text)
		prefix = "\n"
	}
	m := protocol.NewMapper("", []byte(text))
	at, err := m.OffsetRange(offset, offset)
	if err != nil {
		env.TB.Fatal(err)
	}
	env.EditBuffer(file, protocol.TextEdit{
		Range:   at,
		NewText: prefix + fmt.Sprintf("import _ %q // __TEST_PLACEHOLDER_0__\n", paths[0]),
	})
	env.AfterChange()
	importStart := env.RegexpSearch(file, "import _ "+strconv.Quote(paths[0])).Range.Start
	importRange := protocol.Range{Start: importStart, End: protocol.Position{Line: importStart.Line + 1}}

	next := 1
	present := true
	return func() {
		comment := fmt.Sprintf("// __TEST_PLACEHOLDER_%d__", atomic.AddInt64(&editID, 1))
		newText := comment + "\n"
		if present {
			present = false
		} else {
			newText = fmt.Sprintf("import _ %q %s\n", paths[next], comment)
			next = 1 - next
			present = true
		}
		env.EditBuffer(file, protocol.TextEdit{Range: importRange, NewText: newText})
	}
}
