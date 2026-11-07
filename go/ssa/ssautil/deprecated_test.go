// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ssautil_test

// Tests of deprecated public APIs.
// We are keeping some tests around to have some test of the public API.

import (
	"go/parser"
	"os"
	"testing"

	"golang.org/x/tools/go/loader"
	"golang.org/x/tools/go/ssa"
	"golang.org/x/tools/go/ssa/ssautil"
	"golang.org/x/tools/internal/testenv"
)

// TestCreateProgram tests CreateProgram which has an x/tools/go/loader.Program.
func TestCreateProgram(t *testing.T) {
	testenv.NeedsGoBuild(t) // for importer.Default()

	conf := loader.Config{ParserMode: parser.ParseComments}
	f, err := conf.ParseFile("hello.go", hello)
	if err != nil {
		t.Fatal(err)
	}

	conf.CreateFromFiles("main", f)
	iprog, err := conf.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(iprog.Created) != 1 {
		t.Fatalf("Expected 1 Created package. got %d", len(iprog.Created))
	}
	pkg := iprog.Created[0].Pkg

	prog := ssautil.CreateProgram(iprog, ssa.BuilderMode(0))
	ssapkg := prog.Package(pkg)
	ssapkg.Build()

	if pkg.Name() != "main" {
		t.Errorf("pkg.Name() = %s, want main", pkg.Name())
	}
	if ssapkg.Func("main") == nil {
		ssapkg.WriteTo(os.Stderr)
		t.Errorf("ssapkg has no main function")
	}
}
