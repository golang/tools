// Copyright 2021 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package txtar

import (
	"io/fs"
	"sort"
	"strings"
	"testing"
	"testing/fstest"
)

func TestFS(t *testing.T) {
	for _, tc := range []struct{ name, input, files string }{
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
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := Parse([]byte(tc.input))
			files := strings.Fields(tc.files)
			if err := fstest.TestFS(a, files...); err != nil {
				t.Fatal(err)
			}
			for _, name := range files {
				for _, f := range a.Files {
					if f.Name == name {
						b, err := fs.ReadFile(a, name)
						if err != nil {
							t.Fatal(err)
						}
						if string(b) != string(f.Data) {
							t.Fatalf("mismatched contents for %q", name)
						}
					}
				}
			}
			a2, err := From(a)
			if err != nil {
				t.Fatalf("failed to write fsys for %v: %v", tc.name, err)
			}

			if in, out := normalized(a), normalized(a2); in != out {
				t.Errorf("From round trip failed: %q != %q", in, out)
			}

		})
	}
}

func normalized(a *Archive) string {
	a.Comment = nil
	sort.Slice(a.Files, func(i, j int) bool {
		return a.Files[i].Name < a.Files[j].Name
	})
	return string(Format(a))
}
