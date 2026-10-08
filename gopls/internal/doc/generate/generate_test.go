// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"os"
	"testing"

	"golang.org/x/tools/internal/testenv"
)

func TestGenerated(t *testing.T) {
	testenv.NeedsGoPackages(t)
	testenv.NeedsLocalXTools(t)

	ok, err := doMain(false)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Error("documentation needs updating. Run: cd gopls && go generate ./...")
	}
}

// TestAnalyzerTitleEscaped checks that analyzer titles in the
// generated analyzers.md are Markdown-escaped, so that e.g. the "[T]()"
// in reflecttypefor's title is not rendered as an empty link.
func TestAnalyzerTitleEscaped(t *testing.T) {
	data, err := os.ReadFile("../../../doc/analyzers.md")
	if err != nil {
		t.Fatal(err)
	}
	const (
		good = "## `reflecttypefor`: replace reflect.TypeOf(x) with TypeFor\\[T]()\n"
		bad  = "## `reflecttypefor`: replace reflect.TypeOf(x) with TypeFor[T]()\n"
	)
	if bytes.Contains(data, []byte(bad)) {
		t.Errorf("analyzers.md contains unescaped heading: %q", bad)
	}
	if !bytes.Contains(data, []byte(good)) {
		t.Errorf("analyzers.md does not contain escaped heading: %q", good)
	}
}
