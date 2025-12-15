// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package bench

import (
	"context"
	"fmt"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	. "golang.org/x/tools/gopls/internal/test/integration"
	"golang.org/x/tools/gopls/internal/test/integration/fake"
	"golang.org/x/tools/internal/modindex"
)

// experiments show the new code about 15 times faster than the old,
// and the old code sometimes fails to find the completion
func BenchmarkLocalModcache(b *testing.B) {
	budgets := []string{"0s", "100ms", "200ms", "500ms", "1s", "5s"}
	sources := []string{"gopls", "goimports"}
	for _, budget := range budgets {
		b.Run(fmt.Sprintf("budget=%s", budget), func(b *testing.B) {
			for _, source := range sources {
				b.Run(fmt.Sprintf("source=%s", source), func(b *testing.B) {
					runModcacheCompletion(b, budget, source)
				})
			}
		})
	}
}

func runModcacheCompletion(b *testing.B, budget, source string) {
	// First set up the program to be edited
	gomod := `
module mod.com

go 1.21
`
	pat := `
package main
var _ = %s.%s
`
	pkg, name, modcache := findSym(b)
	name, _, _ = strings.Cut(name, " ")
	mainfile := fmt.Sprintf(pat, pkg, name)
	// Second, create the Env and start gopls
	dir := getTempDir()
	if err := os.Mkdir(dir, 0750); err != nil {
		if !os.IsExist(err) {
			b.Fatal(err)
		}
	}
	defer os.RemoveAll(dir) // is this right? needed?
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(gomod), 0644); err != nil {
		b.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(mainfile), 0644); err != nil {
		b.Fatal(err)
	}
	ts, err := newGoplsConnector(nil)
	if err != nil {
		b.Fatal(err)
	}
	// PJW: put better EditorConfig here
	envvars := map[string]string{
		"GOMODCACHE": modcache,
		//"GOPATH":     sandbox.GOPATH(), // do we need a GOPATH?
	}
	fc := fake.EditorConfig{
		Env: envvars,
		Settings: map[string]any{
			"completeUnimported": true,
			"completionBudget":   budget, // "0s", "100ms"
			"importsSource":      source, // "gopls" or "goimports"
		},
	}
	sandbox, editor, awaiter, err := connectEditor(dir, fc, ts)
	if err != nil {
		b.Fatal(err)
	}
	defer sandbox.Close()
	defer editor.Close(context.Background())
	if err := awaiter.Await(context.Background(), InitialWorkspaceLoad); err != nil {
		b.Fatal(err)
	}
	env := &Env{
		TB:      b,
		Ctx:     context.Background(),
		Editor:  editor,
		Sandbox: sandbox,
		Awaiter: awaiter,
	}
	// Check that completion works as expected
	env.CreateBuffer("main.go", mainfile)
	env.AfterChange()
	if false { // warm up? or not?
		loc := env.RegexpSearch("main.go", name)
		completions := env.Completion(loc)
		if len(completions.Items) == 0 {
			b.Fatal("no completions")
		}
	}

	// run benchmark
	for b.Loop() {
		loc := env.RegexpSearch("main.go", name)
		env.Completion(loc)
	}
}

// find some symbol in the module cache
func findSym(t testing.TB) (pkg, name, gomodcache string) {
	initForTest(t)
	cmd := exec.Command("go", "env", "GOMODCACHE")
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	modcache := strings.TrimSpace(string(out))
	ix, err := modindex.Read(modcache)
	if err != nil {
		t.Fatal(err)
	}
	if ix == nil {
		t.Fatal("nil index")
	}
	nth := 100 // or something
	for _, e := range ix.Entries {
		if token.IsExported(e.PkgName) || strings.HasPrefix(e.PkgName, "_") {
			continue // weird stuff in module cache
		}

		for _, nm := range e.Names {
			nth--
			if nth == 0 {
				return e.PkgName, nm, modcache
			}
		}
	}
	t.Fatalf("index doesn't have enough usable names, need another %d", nth)
	return "", "", modcache
}

// Set IndexDir, avoiding the special case for tests,
func initForTest(t testing.TB) {
	dir, err := os.UserCacheDir()
	if err != nil {
		t.Fatalf("os.UserCacheDir: %v", err)
	}
	dir = filepath.Join(dir, "go", "imports")
	modindex.IndexDir = dir
}
