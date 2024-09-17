// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
package bench

import (
	"encoding/json"
	"testing"

	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/gopls/internal/protocol/command"
	"golang.org/x/tools/gopls/internal/test/integration"
)

func BenchmarkPackagesCommand(b *testing.B) {
	// By convention, x/benchmarks runs the gopls benchmarks with -short, so that
	// we can use this flag to filter out benchmarks that should not be run by
	// the perf builder.
	//
	// In this case, the benchmark must be skipped because the current baseline
	// (gopls@v0.11.0) lacks the gopls.package command.
	if testing.Short() {
		b.Skip("not supported by the benchmark dashboard baseline")
	}

	tests := []struct {
		repo    string
		files   []string
		recurse bool
	}{
		{"tools", []string{"internal/lsp/debounce_test.go"}, false},
	}
	for _, test := range tests {
		b.Run(test.repo, func(b *testing.B) {
			args := command.PackagesArgs{
				Mode: command.NeedTests,
			}

			env := getRepo(b, test.repo).sharedEnv(b)
			for _, file := range test.files {
				env.OpenFile(file)
				defer closeBuffer(b, env, file)
				args.Files = append(args.Files, env.Editor.DocumentURI(file))
			}
			env.AfterChange()

			result := executePackagesCmd(b, env, args) // pre-warm

			// sanity check JSON {en,de}coding
			var pkgs command.PackagesResult
			data, err := json.Marshal(result)
			if err != nil {
				b.Fatal(err)
			}
			err = json.Unmarshal(data, &pkgs)
			if err != nil {
				b.Fatal(err)
			}
			var haveTest bool
			for _, pkg := range pkgs.Packages {
				for _, file := range pkg.TestFiles {
					if len(file.Tests) > 0 {
						haveTest = true
						break
					}
				}
			}
			if !haveTest {
				b.Fatalf("Expected tests")
			}

			b.ResetTimer()

			if stopAndRecord := startProfileIfSupported(b, env, qualifiedName(test.repo, "packages")); stopAndRecord != nil {
				defer stopAndRecord()
			}

			for i := 0; i < b.N; i++ {
				executePackagesCmd(b, env, args)
			}
		})
	}
}

func executePackagesCmd(t testing.TB, env *integration.Env, args command.PackagesArgs) any {
	t.Helper()
	cmd := command.NewPackagesCommand("Packages", args)
	result, err := env.Editor.Server.ExecuteCommand(env.Ctx, &protocol.ExecuteCommandParams{
		Command:   command.Packages.String(),
		Arguments: cmd.Arguments,
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}
