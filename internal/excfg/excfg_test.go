// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package excfg

import (
	"bytes"
	"flag"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/tools/go/cfg"
	"golang.org/x/tools/txtar"
)

var update = flag.Bool("update", false, "update expected output")

func TestExCFG(t *testing.T) {
	files, err := filepath.Glob("testdata/*.txt")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("no test files found in testdata/")
	}

	for _, file := range files {
		t.Run(filepath.Base(file), func(t *testing.T) {
			data, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			ar := txtar.Parse(data)
			var src, want []byte
			for _, f := range ar.Files {
				switch f.Name {
				case "src.go":
					src = f.Data
				case "want":
					want = bytes.TrimSpace(f.Data)
				}
			}

			if src == nil {
				t.Fatal("missing src.go in test file")
			}

			fset := token.NewFileSet()
			f, err := parser.ParseFile(fset, "src.go", src, 0)
			if err != nil {
				t.Fatal(err)
			}

			// Find function body.
			var body *ast.BlockStmt
			for _, decl := range f.Decls {
				if fd, ok := decl.(*ast.FuncDecl); ok && fd.Name.Name == "main" {
					body = fd.Body
					break
				}
			}

			if body == nil {
				t.Fatal("no main function found")
			}

			c := cfg.New(body, func(call *ast.CallExpr) bool { return true })
			ec := New(c, fset)
			got := strings.TrimSpace(ec.String())

			if *update {
				found := false
				for i := range ar.Files {
					if ar.Files[i].Name == "want" {
						ar.Files[i].Data = []byte(got)
						found = true
						break
					}
				}
				if !found {
					ar.Files = append(ar.Files, txtar.File{
						Name: "want",
						Data: []byte(got),
					})
				}
				if err := os.WriteFile(file, txtar.Format(ar), 0644); err != nil {
					t.Fatal(err)
				}
				return
			}

			if want == nil {
				t.Logf("Output for %s:\n%s", file, got)
				t.Errorf("missing expected output")
			} else if got != string(want) {
				t.Errorf("mismatch:\ngot:\n%s\nwant:\n%s", got, want)
			}
		})
	}
}
