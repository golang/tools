// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// go1.23 is required for os.CopyFS.
// !android is required for compatibility with endtoend_test.go.
//go:build go1.23 && !android

package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/tools/internal/diffp"
	"golang.org/x/tools/internal/testenv"
	"golang.org/x/tools/txtar"
)

// This file contains a test that checks the output files existence
// and content when stringer has types from multiple different input
// files to choose from.
//
// Input is specified in a txtar string.

// Several tests expect the type Foo generated in some package.
func expectFooString(pkg string) []byte {
	return []byte(fmt.Sprintf(`
// Header comment ignored.

package %s

import "strconv"

func _() {
	// An "invalid array index" compiler error signifies that the constant values have changed.
	// Re-run the stringer command to generate them again.
	var x [1]struct{}
	_ = x[fooX-0]
	_ = x[fooY-1]
	_ = x[fooZ-2]
}

const _Foo_name = "fooXfooYfooZ"

var _Foo_index = [...]uint8{0, 4, 8, 12}

func (i Foo) String() string {
	if i < 0 || i >= Foo(len(_Foo_index)-1) {
		return "Foo(" + strconv.FormatInt(int64(i), 10) + ")"
	}
	return _Foo_name[_Foo_index[i]:_Foo_index[i+1]]
}`, pkg))
}

func TestMultifileStringer(t *testing.T) {
	testenv.NeedsTool(t, "go")
	stringer := stringerPath(t)

	tests := []struct {
		name        string
		args        []string
		archive     []byte
		expectFiles map[string][]byte
	}{
		{
			name: "package only",
			args: []string{"-type=Foo"},
			archive: []byte(`
-- go.mod --
module foo

-- main.go --
package main

type Foo int

const (
	fooX Foo = iota
	fooY
	fooZ
)`),
			expectFiles: map[string][]byte{
				"foo_string.go": expectFooString("main"),
			},
		},
		{
			name: "test package only",
			args: []string{"-type=Foo"},
			archive: []byte(`
-- go.mod --
module foo

-- main.go --
package main

func main() {}

-- main_test.go --
package main

type Foo int

const (
	fooX Foo = iota
	fooY
	fooZ
)`),
			expectFiles: map[string][]byte{
				"foo_string_test.go": expectFooString("main"),
			},
		},
		{
			name: "x_test package only",
			args: []string{"-type=Foo"},
			archive: []byte(`
-- go.mod --
module foo

-- main.go --
package main

func main() {}

-- main_test.go --
package main_test

type Foo int

const (
	fooX Foo = iota
	fooY
	fooZ
)`),
			expectFiles: map[string][]byte{
				"foo_string_test.go": expectFooString("main_test"),
			},
		},
		{
			// Re-declaring the type in a less prioritized package does not change our output.
			name: "package over test package",
			args: []string{"-type=Foo"},
			archive: []byte(`
-- go.mod --
module foo

-- main.go --
package main

type Foo int

const (
	fooX Foo = iota
	fooY
	fooZ
)

-- main_test.go --
package main

type Foo int

const (
	otherX Foo = iota
	otherY
	otherZ
)
`),
			expectFiles: map[string][]byte{
				"foo_string.go": expectFooString("main"),
			},
		},
		{
			// Re-declaring the type in a less prioritized package does not change our output.
			name: "package over x_test package",
			args: []string{"-type=Foo"},
			archive: []byte(`
-- go.mod --
module foo

-- main.go --
package main

type Foo int

const (
	fooX Foo = iota
	fooY
	fooZ
)

-- main_test.go --
package main_test

type Foo int

const (
	otherX Foo = iota
	otherY
	otherZ
)
`),
			expectFiles: map[string][]byte{
				"foo_string.go": expectFooString("main"),
			},
		},
		{
			// Re-declaring the type in a less prioritized package does not change our output.
			name: "test package over x_test package",
			args: []string{"-type=Foo"},
			archive: []byte(`
-- go.mod --
module foo

-- main.go --
package main

-- main_test.go --
package main

type Foo int

const (
	fooX Foo = iota
	fooY
	fooZ
)

-- main_pkg_test.go --
package main_test

type Foo int

const (
	otherX Foo = iota
	otherY
	otherZ
)`),
			expectFiles: map[string][]byte{
				"foo_string_test.go": expectFooString("main"),
			},
		},
		{
			name: "unique type in each package variant",
			args: []string{"-type=Foo,Bar,Baz"},
			archive: []byte(`
-- go.mod --
module foo

-- main.go --
package main

type Foo int

const fooX Foo = 1

-- main_test.go --
package main

type Bar int

const barX Bar = 1

-- main_pkg_test.go --
package main_test

type Baz int

const bazX Baz = 1
`),
			expectFiles: map[string][]byte{
				"foo_string.go": []byte(`
// Header comment ignored.

package main

import "strconv"

func _() {
	// An "invalid array index" compiler error signifies that the constant values have changed.
	// Re-run the stringer command to generate them again.
	var x [1]struct{}
	_ = x[fooX-1]
}

const _Foo_name = "fooX"

var _Foo_index = [...]uint8{0, 4}

func (i Foo) String() string {
	i -= 1
	if i < 0 || i >= Foo(len(_Foo_index)-1) {
		return "Foo(" + strconv.FormatInt(int64(i+1), 10) + ")"
	}
	return _Foo_name[_Foo_index[i]:_Foo_index[i+1]]
}`),

				"bar_string_test.go": []byte(`
// Header comment ignored.

package main

import "strconv"

func _() {
	// An "invalid array index" compiler error signifies that the constant values have changed.
	// Re-run the stringer command to generate them again.
	var x [1]struct{}
	_ = x[barX-1]
}

const _Bar_name = "barX"

var _Bar_index = [...]uint8{0, 4}

func (i Bar) String() string {
	i -= 1
	if i < 0 || i >= Bar(len(_Bar_index)-1) {
		return "Bar(" + strconv.FormatInt(int64(i+1), 10) + ")"
	}
	return _Bar_name[_Bar_index[i]:_Bar_index[i+1]]
}`),

				"baz_string_test.go": []byte(`
// Header comment ignored.

package main_test

import "strconv"

func _() {
	// An "invalid array index" compiler error signifies that the constant values have changed.
	// Re-run the stringer command to generate them again.
	var x [1]struct{}
	_ = x[bazX-1]
}

const _Baz_name = "bazX"

var _Baz_index = [...]uint8{0, 4}

func (i Baz) String() string {
	i -= 1
	if i < 0 || i >= Baz(len(_Baz_index)-1) {
		return "Baz(" + strconv.FormatInt(int64(i+1), 10) + ")"
	}
	return _Baz_name[_Baz_index[i]:_Baz_index[i+1]]
}`),
			},
		},

		{
			name: "package over test package with custom output",
			args: []string{"-type=Foo", "-output=custom_output.go"},
			archive: []byte(`
-- go.mod --
module foo

-- main.go --
package main

type Foo int

const (
	fooX Foo = iota
	fooY
	fooZ
)

-- main_test.go --
package main

type Foo int

const (
	otherX Foo = iota
	otherY
	otherZ
)`),
			expectFiles: map[string][]byte{
				"custom_output.go": expectFooString("main"),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()

			arFS, err := txtar.FS(txtar.Parse(tt.archive))
			if err != nil {
				t.Fatalf("txtar.FS: %s", err)
			}
			err = os.CopyFS(tmpDir, arFS)
			if err != nil {
				t.Fatalf("copy fs: %s", err)
			}
			before := dirContent(t, tmpDir)

			// Must run stringer in the temp directory, see TestTags.
			args := append(tt.args, tmpDir)
			err = runInDir(t, tmpDir, stringer, args...)
			if err != nil {
				t.Fatalf("run stringer: %s", err)
			}

			// Check that all !path files have been created with the expected content.
			for f, want := range tt.expectFiles {
				got, err := os.ReadFile(filepath.Join(tmpDir, f))
				if errors.Is(err, os.ErrNotExist) {
					t.Errorf("expected file not written during test: %s", f)
					continue
				}
				if err != nil {
					t.Fatalf("read file %q: %s", f, err)
				}
				// Trim data for more robust comparison.
				got = trimHeader(bytes.TrimSpace(got))
				want = trimHeader(bytes.TrimSpace(want))
				if !bytes.Equal(want, got) {
					t.Errorf("file %s does not have the expected content:\n%s", f, diffp.Diff("want", want, "got", got))
				}
			}

			// Check that nothing else has been created.
			after := dirContent(t, tmpDir)
			for f := range after {
				if _, expected := tt.expectFiles[f]; !expected && !before[f] {
					t.Errorf("found %q in output directory, it is neither input or expected output", f)
				}
			}

		})
	}
}

func dirContent(t *testing.T, dir string) map[string]bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %s", err)
	}

	out := map[string]bool{}
	for _, e := range entries {
		out[e.Name()] = true
	}
	return out
}

// trimHeader that stringer puts in file.
// It depends on location and interferes with comparing file content.
func trimHeader(s []byte) []byte {
	if !bytes.HasPrefix(s, []byte("//")) {
		return s
	}
	_, after, ok := bytes.Cut(s, []byte{'\n'})
	if ok {
		return after
	}
	return s
}
