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
	"path/filepath"
	"regexp"
	"slices"
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

	// Parse the file header only, with comments.
	//
	// Parsing only the header avoids spurious parse errors
	// due to version skew when the gopls module relies on
	// a newer version of Go than x/tools.
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, filename, nil, parser.PackageClauseOnly|parser.ParseComments)
	if err != nil {
		return false, err
	}

	// Don't require headers on generated files.
	if ast.IsGenerated(f) {
		return false, nil
	}

	// The copyright must appear before the package declaration.
	return !slices.ContainsFunc(f.Comments, func(c *ast.CommentGroup) bool {
		return copyrightRe.MatchString(c.Text())
	}), nil
}
