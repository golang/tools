// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package txtar

import (
	"io/fs"
	"path"
	"sort"
	"strings"
	"testing"
	"testing/fstest"
)

func TestFS(t *testing.T) {
	var fstestcases = []struct {
		name, input, files string
		invalidNames       bool
	}{
		{
			name:  "empty",
			input: ``,
			files: "",
		},
		{
			name: "one",
			input: `
-- one.txt --
one
`,
			files: "one.txt",
		},
		{
			name: "two",
			input: `
-- one.txt --
one
-- two.txt --
two
`,
			files: "one.txt two.txt",
		},
		{
			name: "subdirectories",
			input: `
-- one.txt --
one
-- 2/two.txt --
two
-- 2/3/three.txt --
three
-- 4/four.txt --
three
`,
			files: "one.txt 2/two.txt 2/3/three.txt 4/four.txt",
		},
		{
			name: "unclean file names",
			input: `
-- 1/../one.txt --
one
-- 2/sub/../two.txt --
two
`,
			invalidNames: true,
		},
		{
			name: "overlapping names",
			input: `
-- 1/../one.txt --
one
-- 2/../one.txt --
two
`,
			files:        "one.txt",
			invalidNames: true,
		},
		{
			name: "invalid name",
			input: `
-- ../one.txt --
one
`,
			invalidNames: true,
		},
	}

	for _, tc := range fstestcases {
		t.Run(tc.name, func(t *testing.T) {
			files := strings.Fields(tc.files)
			a := Parse([]byte(tc.input))
			fsys, err := FS(a)
			if tc.invalidNames {
				if err == nil {
					t.Fatal("expected error: got nil")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := fstest.TestFS(fsys, files...); err != nil {
				t.Fatal(err)
			}

			for _, f := range a.Files {
				b, err := fs.ReadFile(fsys, f.Name)
				if err != nil {
					t.Fatalf("could not read %s from fsys: %v", f.Name, err)
				}
				if string(b) != string(f.Data) {
					t.Fatalf("mismatched contents for %q", f.Name)
				}
			}
			a2, err := From(fsys)
			if err != nil {
				t.Fatalf("error building Archive from fsys: %v", err)
			}

			if in, out := normalized(a), normalized(a2); in != out {
				t.Error("did not round trip")
			}
		})
	}
}

func normalized(a *Archive) string {
	a.Comment = nil
	for i := range a.Files {
		f := &a.Files[i]
		f.Name = path.Clean(f.Name)
	}
	sort.Slice(a.Files, func(i, j int) bool {
		return a.Files[i].Name < a.Files[j].Name
	})
	return string(Format(a))
}
