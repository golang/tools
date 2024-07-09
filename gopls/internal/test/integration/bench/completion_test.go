// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package bench

import (
	"flag"
	"fmt"
	"sync/atomic"
	"testing"

	"golang.org/x/tools/gopls/internal/protocol"
	. "golang.org/x/tools/gopls/internal/test/integration"
	"golang.org/x/tools/gopls/internal/test/integration/fake"
)

var completionGOPATH = flag.String("completion_gopath", "", "if set, use this GOPATH for BenchmarkCompletion")

type completionBenchOptions struct {
	file, locationRegexp string

	// Hooks to run edits before initial completion
	setup            func(*Env) // run before the benchmark starts
	beforeCompletion func(*Env) // run before each completion
}

// Deprecated: new tests should be expressed in BenchmarkCompletion.
func benchmarkCompletion(options completionBenchOptions, b *testing.B) {
	repo := getRepo(b, "tools")
	_ = repo.sharedEnv(b) // ensure cache is warm
	env := repo.newEnv(b, fake.EditorConfig{}, "completion", false)
	defer env.Close()

	// Run edits required for this completion.
	if options.setup != nil {
		options.setup(env)
	}

	// Run a completion to make sure the system is warm.
	loc := env.RegexpSearch(options.file, options.locationRegexp)
	completions := env.Completion(loc)

	if testing.Verbose() {
		fmt.Println("Results:")
		for i := 0; i < len(completions.Items); i++ {
			fmt.Printf("\t%d. %v\n", i, completions.Items[i])
		}
	}

	b.Run("tools", func(b *testing.B) {
		if stopAndRecord := startProfileIfSupported(b, env, qualifiedName("tools", "completion")); stopAndRecord != nil {
			defer stopAndRecord()
		}

		for i := 0; i < b.N; i++ {
			if options.beforeCompletion != nil {
				options.beforeCompletion(env)
			}
			env.Completion(loc)
		}
	})
}

// endRangeInBuffer returns the position for last character in the buffer for
// the given file.
func endRangeInBuffer(env *Env, name string) protocol.Range {
	buffer := env.BufferText(name)
	m := protocol.NewMapper("", []byte(buffer))
	rng, err := m.OffsetRange(len(buffer), len(buffer))
	if err != nil {
		env.T.Fatal(err)
	}
	return rng
}

// Benchmark struct completion in tools codebase.
func BenchmarkStructCompletion(b *testing.B) {
	file := "internal/lsp/cache/session.go"

	setup := func(env *Env) {
		env.OpenFile(file)
		env.EditBuffer(file, protocol.TextEdit{
			Range:   endRangeInBuffer(env, file),
			NewText: "\nvar testVariable map[string]bool = Session{}.\n",
		})
	}

	benchmarkCompletion(completionBenchOptions{
		file:           file,
		locationRegexp: `var testVariable map\[string\]bool = Session{}(\.)`,
		setup:          setup,
	}, b)
}

// Benchmark import completion in tools codebase.
func BenchmarkImportCompletion(b *testing.B) {
	const file = "internal/lsp/source/completion/completion.go"
	benchmarkCompletion(completionBenchOptions{
		file:           file,
		locationRegexp: `go\/()`,
		setup:          func(env *Env) { env.OpenFile(file) },
	}, b)
}

// Benchmark slice completion in tools codebase.
func BenchmarkSliceCompletion(b *testing.B) {
	file := "internal/lsp/cache/session.go"

	setup := func(env *Env) {
		env.OpenFile(file)
		env.EditBuffer(file, protocol.TextEdit{
			Range:   endRangeInBuffer(env, file),
			NewText: "\nvar testVariable []byte = \n",
		})
	}

	benchmarkCompletion(completionBenchOptions{
		file:           file,
		locationRegexp: `var testVariable \[\]byte (=)`,
		setup:          setup,
	}, b)
}

// Benchmark deep completion in function call in tools codebase.
func BenchmarkFuncDeepCompletion(b *testing.B) {
	file := "internal/lsp/source/completion/completion.go"
	fileContent := `
func (c *completer) _() {
	c.inference.kindMatches(c.)
}
`
	setup := func(env *Env) {
		env.OpenFile(file)
		originalBuffer := env.BufferText(file)
		env.EditBuffer(file, protocol.TextEdit{
			Range: endRangeInBuffer(env, file),
			// TODO(rfindley): this is a bug: it should just be fileContent.
			NewText: originalBuffer + fileContent,
		})
	}

	benchmarkCompletion(completionBenchOptions{
		file:           file,
		locationRegexp: `func \(c \*completer\) _\(\) {\n\tc\.inference\.kindMatches\((c)`,
		setup:          setup,
	}, b)
}

type completionTest struct {
	repo           string
	name           string
	file           string // repo-relative file to create
	content        string // file content
	locationRegexp string // regexp for completion
}

var completionTests = []completionTest{
	{
		"tools",
		"selector",
		"internal/lsp/source/completion/completion2.go",
		`
package completion

func (c *completer) _() {
	c.inference.kindMatches(c.)
}
`,
		`func \(c \*completer\) _\(\) {\n\tc\.inference\.kindMatches\((c)`,
	},
	{
		"tools",
		"unimportedident",
		"internal/lsp/source/completion/completion2.go",
		`
package completion

func (c *completer) _() {
	lo
}
`,
		`lo()`,
	},
	{
		"tools",
		"unimportedselector",
		"internal/lsp/source/completion/completion2.go",
		`
package completion

func (c *completer) _() {
	log.
}
`,
		`log\.()`,
	},
	{
		"kubernetes",
		"selector",
		"pkg/kubelet/kubelet2.go",
		`
package kubelet

func (kl *Kubelet) _() {
	kl.
}
`,
		`kl\.()`,
	},
	{
		"kubernetes",
		"identifier",
		"pkg/kubelet/kubelet2.go",
		`
package kubelet

func (kl *Kubelet) _() {
	k // here
}
`,
		`k() // here`,
	},
	{
		"oracle",
		"selector",
		"dataintegration/pivot2.go",
		`
package dataintegration

func (p *Pivot) _() {
	p.
}
`,
		`p\.()`,
	},
}

// Benchmark completion following an arbitrary edit.
//
// Edits force type-checked packages to be invalidated, so we want to measure
// how long it takes before completion results are available.
func BenchmarkCompletion(b *testing.B) {
	for _, test := range completionTests {
		b.Run(fmt.Sprintf("%s_%s", test.repo, test.name), func(b *testing.B) {
			for _, followingEdit := range []bool{true, false} {
				b.Run(fmt.Sprintf("edit=%v", followingEdit), func(b *testing.B) {
					for _, completeUnimported := range []bool{true, false} {
						b.Run(fmt.Sprintf("unimported=%v", completeUnimported), func(b *testing.B) {
							for _, budget := range []string{"0s", "100ms"} {
								b.Run(fmt.Sprintf("budget=%s", budget), func(b *testing.B) {
									runCompletion(b, test, followingEdit, completeUnimported, budget)
								})
							}
						})
					}
				})
			}
		})
	}
}

// For optimizing unimported completion, it can be useful to benchmark with a
// huge GOMODCACHE.
var gomodcache = flag.String("gomodcache", "", "optional GOMODCACHE for unimported completion benchmarks")

func runCompletion(b *testing.B, test completionTest, followingEdit, completeUnimported bool, budget string) {
	repo := getRepo(b, test.repo)
	gopath := *completionGOPATH
	if gopath == "" {
		// use a warm GOPATH
		sharedEnv := repo.sharedEnv(b)
		gopath = sharedEnv.Sandbox.GOPATH()
	}
	envvars := map[string]string{
		"GOPATH": gopath,
	}

	if *gomodcache != "" {
		envvars["GOMODCACHE"] = *gomodcache
	}

	env := repo.newEnv(b, fake.EditorConfig{
		Env: envvars,
		Settings: map[string]interface{}{
			"completeUnimported": completeUnimported,
			"completionBudget":   budget,
		},
	}, "completion", false)
	defer env.Close()

	env.CreateBuffer(test.file, "// __TEST_PLACEHOLDER_0__\n"+test.content)
	editPlaceholder := func() {
		edits := atomic.AddInt64(&editID, 1)
		env.EditBuffer(test.file, protocol.TextEdit{
			Range: protocol.Range{
				Start: protocol.Position{Line: 0, Character: 0},
				End:   protocol.Position{Line: 1, Character: 0},
			},
			// Increment the placeholder text, to ensure cache misses.
			NewText: fmt.Sprintf("// __TEST_PLACEHOLDER_%d__\n", edits),
		})
	}
	env.AfterChange()

	// Run a completion to make sure the system is warm.
	loc := env.RegexpSearch(test.file, test.locationRegexp)
	completions := env.Completion(loc)

	if testing.Verbose() {
		fmt.Println("Results:")
		for i, item := range completions.Items {
			fmt.Printf("\t%d. %v\n", i, item)
		}
	}

	b.ResetTimer()

	if stopAndRecord := startProfileIfSupported(b, env, qualifiedName(test.repo, "completion")); stopAndRecord != nil {
		defer stopAndRecord()
	}

	for i := 0; i < b.N; i++ {
		if followingEdit {
			editPlaceholder()
		}
		loc := env.RegexpSearch(test.file, test.locationRegexp)
		env.Completion(loc)
	}
}
