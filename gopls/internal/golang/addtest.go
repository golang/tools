// Copyright 2019 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package golang

// This file defines the behavior of the "Add test for FUNC" command.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"go/token"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/tools/gopls/internal/cache"
	"golang.org/x/tools/gopls/internal/cache/parsego"
	"golang.org/x/tools/gopls/internal/protocol"
)

// AddTestForFunc adds a test for the function enclosing the given input range.
// It creates a _test.go file if one does not already exist.
func AddTestForFunc(ctx context.Context, snapshot *cache.Snapshot, loc protocol.Location) (changes []protocol.DocumentChange, _ error) {
	pkg, pgf, err := NarrowestPackageForFile(ctx, snapshot, loc.URI)
	if err != nil {
		return nil, err
	}

	testBase := strings.TrimSuffix(filepath.Base(loc.URI.Path()), ".go") + "_test.go"
	goTestFileURI := protocol.URIFromPath(filepath.Join(loc.URI.Dir().Path(), testBase))

	testFH, err := snapshot.ReadFile(ctx, goTestFileURI)
	if err != nil {
		return nil, err
	}

	// TODO(hxjiang): use a fresh name if the same test function name already
	// exist.

	var (
		// edits contains all the text edits to be applied to the test file.
		edits []protocol.TextEdit
		// header is the buffer containing the text edit to the beginning of the file.
		header bytes.Buffer
	)

	testPgf, err := snapshot.ParseGo(ctx, testFH, parsego.Header)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}

		changes = append(changes, protocol.DocumentChangeCreate(goTestFileURI))

		// If this test file was created by the gopls, add a copyright header based
		// on the originating file.
		// Search for something that looks like a copyright header, to replicate
		// in the new file.
		// TODO(hxjiang): should we refine this heuristic, for example by checking for
		// the word 'copyright'?
		if groups := pgf.File.Comments; len(groups) > 0 {
			// Copyright should appear before package decl and must be the first
			// comment group.
			// Avoid copying any other comment like package doc or directive comment.
			if c := groups[0]; c.Pos() < pgf.File.Package && c != pgf.File.Doc &&
				!isDirective(groups[0].List[0].Text) {
				start, end, err := pgf.NodeOffsets(c)
				if err != nil {
					return nil, err
				}
				header.Write(pgf.Src[start:end])
				// One empty line between copyright header and package decl.
				header.WriteString("\n\n")
			}
		}
	}

	// If the test file does not have package decl, use the originating file to
	// determine a package decl for the new file. Prefer xtest package.s
	if testPgf == nil || testPgf.File == nil || testPgf.File.Package == token.NoPos {
		// One empty line between package decl and rest of the file.
		fmt.Fprintf(&header, "package %s_test\n\n", pkg.Types().Name())
	}

	// Write the copyright and package decl to the beginning of the file.
	if text := header.String(); len(text) != 0 {
		edits = append(edits, protocol.TextEdit{
			Range:   protocol.Range{},
			NewText: text,
		})
	}

	// TODO(hxjiang): reject if the function/method is unexported.
	// TODO(hxjiang): modify existing imports or add new imports.

	// If the parse go file is missing, the fileEnd is the file start (zero value).
	fileEnd := protocol.Range{}
	if testPgf != nil {
		fileEnd, err = testPgf.PosRange(testPgf.File.FileEnd, testPgf.File.FileEnd)
		if err != nil {
			return nil, err
		}
	}

	// test is the buffer containing the text edit to the test function.
	var test bytes.Buffer
	// TODO(hxjiang): replace test foo function with table-driven test.
	test.WriteString("\nfunc TestFoo(*testing.T) {}")
	edits = append(edits, protocol.TextEdit{
		Range:   fileEnd,
		NewText: test.String(),
	})
	return append(changes, protocol.DocumentChangeEdit(testFH, edits)), nil
}
