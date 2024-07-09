// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package workspace

import (
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	. "golang.org/x/tools/gopls/internal/test/integration"
)

func TestStdWorkspace(t *testing.T) {
	// This test checks that we actually load workspace packages when opening
	// GOROOT.
	//
	// In golang/go#65801, we failed to do this because go/packages returns nil
	// Module for std and cmd.
	//
	// Because this test loads std as a workspace, it may be slow on smaller
	// builders.
	if testing.Short() {
		t.Skip("skipping with -short: loads GOROOT")
	}

	// The test also fails on Windows because an absolute path does not match
	// (likely a misspelling due to slashes).
	// TODO(rfindley): investigate and fix this on windows.
	if runtime.GOOS == "windows" {
		t.Skip("skipping on windows: fails to misspelled paths")
	}

	// Query GOROOT. This is slightly more precise than e.g. runtime.GOROOT, as
	// it queries the go command in the environment.
	goroot, err := exec.Command("go", "env", "GOROOT").Output()
	if err != nil {
		t.Fatal(err)
	}
	stdDir := filepath.Join(strings.TrimSpace(string(goroot)), "src")
	WithOptions(
		Modes(Default), // This test may be slow. No reason to run it multiple times.
		WorkspaceFolders(stdDir),
	).Run(t, "", func(t *testing.T, env *Env) {
		// Find parser.ParseFile. Query with `'` to get an exact match.
		syms := env.Symbol("'go/parser.ParseFile")
		if len(syms) != 1 {
			t.Fatalf("got %d symbols, want exactly 1. Symbols:\n%v", len(syms), syms)
		}
		parserPath := syms[0].Location.URI.Path()
		env.OpenFile(parserPath)

		// Find the reference to ast.File from the signature of ParseFile. This
		// helps guard against matching a comment.
		astFile := env.RegexpSearch(parserPath, `func ParseFile\(.*ast\.(File)`)
		refs := env.References(astFile)

		// If we've successfully loaded workspace packages for std, we should find
		// a reference in go/types.
		foundGoTypesReference := false
		for _, ref := range refs {
			if strings.Contains(string(ref.URI), "go/types") {
				foundGoTypesReference = true
			}
		}
		if !foundGoTypesReference {
			t.Errorf("references(ast.File) did not return a go/types reference. Refs:\n%v", refs)
		}
	})
}
