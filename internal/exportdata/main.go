// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build ignore

// This program writes the golden files for TestCompatibility: the
// export data for testdata/golden/src/ref, as written by the current
// encoder. Run it (go run main.go) when the encoder starts to emit a
// new version. It never overwrites an existing file.
package main

import (
	"encoding/binary"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"log"
	"os"
	"path/filepath"

	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/internal/exportdata"
)

func main() {
	const dir = "testdata/golden"
	src, err := filepath.Abs(filepath.Join(dir, "src"))
	if err != nil {
		log.Fatal(err)
	}
	cfg := &packages.Config{
		Mode: packages.LoadAllSyntax,
		Dir:  src,
		Env:  append(os.Environ(), "GOWORK=off"),
		// Record short file names (e.g. ref/ref.go) in the export
		// data, as TestCompatibility expects.
		ParseFile: func(fset *token.FileSet, filename string, data []byte) (*ast.File, error) {
			rel, err := filepath.Rel(src, filename)
			if err != nil {
				return nil, err
			}
			return parser.ParseFile(fset, filepath.ToSlash(rel), data, parser.SkipObjectResolution)
		},
	}
	pkgs, err := packages.Load(cfg, "example.com/ref")
	if err != nil {
		log.Fatal(err)
	}
	if packages.PrintErrors(pkgs) > 0 {
		os.Exit(1)
	}
	ref := pkgs[0]

	for _, shallow := range []bool{false, true} {
		data, err := exportdata.Encode(ref.Fset, ref.Types, exportdata.EncodeOptions{Shallow: shallow})
		if err != nil {
			log.Fatal(err)
		}
		mode := "deep"
		if shallow {
			mode = "shallow"
		}
		version, _ := binary.Uvarint(data[1:])
		filename := filepath.Join(dir, fmt.Sprintf("v%d.%s.i", version, mode))
		if _, err := os.Stat(filename); err == nil {
			log.Printf("not overwriting existing %s", filename)
			continue
		}
		if err := os.WriteFile(filename, data, 0o666); err != nil {
			log.Fatal(err)
		}
		log.Printf("wrote %s", filename)
	}
}
