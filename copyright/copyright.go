// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package copyright checks that files have the correct copyright notices.
package copyright

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// (used only by tests)
func checkCopyright(dir string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			// Skip directories like ".git".
			if strings.HasPrefix(d.Name(), ".") && d.Name() != "." && d.Name() != ".." {
				return filepath.SkipDir
			}
			// Skip any directory that starts with an underscore, as the go
			// command would.
			if strings.HasPrefix(d.Name(), "_") {
				return filepath.SkipDir
			}
			return nil
		}
		needsCopyright, err := checkFile(dir, path)
		if err != nil {
			return err
		}
		if needsCopyright {
			files = append(files, path)
		}
		return nil
	})
	return files, err
}

var copyrightRe = regexp.MustCompile(`Copyright \d{4} The Go Authors. All rights reserved.
Use of this source code is governed by a BSD-style
license that can be found in the LICENSE file.`)

func checkFile(toolsDir, filename string) (bool, error) {
	// Only check Go files.
	if !strings.HasSuffix(filename, ".go") {
		return false, nil
	}
	// Don't check testdata files.
	normalized := strings.TrimPrefix(filepath.ToSlash(filename), filepath.ToSlash(toolsDir))
	if strings.Contains(normalized, "/testdata/") {
		return false, nil
	}
	// goyacc is the only file with a different copyright header.
	if strings.HasSuffix(normalized, "cmd/goyacc/yacc.go") {
		return false, nil
	}
	content, err := os.ReadFile(filename)
	if err != nil {
		return false, err
	}
	fset := token.NewFileSet()
	parsed, err := parser.ParseFile(fset, filename, content, parser.ParseComments)
	if err != nil {
		return false, err
	}
	// Don't require headers on generated files.
	if ast.IsGenerated(parsed) {
		return false, nil
	}
	shouldAddCopyright := true
	for _, c := range parsed.Comments {
		// The copyright should appear before the package declaration.
		if c.Pos() > parsed.Package {
			break
		}
		if copyrightRe.MatchString(c.Text()) {
			shouldAddCopyright = false
			break
		}
	}
	return shouldAddCopyright, nil
}
