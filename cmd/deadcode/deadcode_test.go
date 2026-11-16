// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main_test

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/tools/internal/testenv"
	"golang.org/x/tools/txtar"
)

// Test runs the deadcode command on each scenario
// described by a testdata/*.txtar file.
func Test(t *testing.T) {
	testenv.NeedsTool(t, "go")
	if runtime.GOOS == "android" {
		t.Skipf("the dependencies are not available on android")
	}

	exe := buildDeadcode(t)

	matches, err := filepath.Glob("testdata/*.txtar")
	if err != nil {
		t.Fatal(err)
	}
	for _, filename := range matches {
		t.Run(filename, func(t *testing.T) {
			t.Parallel()

			ar, err := txtar.ParseFile(filename)
			if err != nil {
				t.Fatal(err)
			}

			// Write the archive files to the temp directory.
			tmpdir := extractTxtar(t, ar)

			// Parse archive comment as directives of these forms:
			//
			//  [!]deadcode args...	command-line arguments
			//  [!]want arg		expected/unwanted string in output (or stderr)
			//
			// Args may be Go-quoted strings.
			type testcase struct {
				linenum int
				args    []string
				wantErr bool
				want    map[string]bool // string -> sense
			}
			var cases []*testcase
			var current *testcase
			for i, line := range strings.Split(string(ar.Comment), "\n") {
				line = strings.TrimSpace(line)
				if line == "" || line[0] == '#' {
					continue // skip blanks and comments
				}

				words, err := words(line)
				if err != nil {
					t.Fatalf("cannot break line into words: %v (%s)", err, line)
				}
				switch kind := words[0]; kind {
				case "deadcode", "!deadcode":
					current = &testcase{
						linenum: i + 1,
						want:    make(map[string]bool),
						args:    words[1:],
						wantErr: kind[0] == '!',
					}
					cases = append(cases, current)
				case "want", "!want":
					if current == nil {
						t.Fatalf("'want' directive must be after 'deadcode'")
					}
					if len(words) != 2 {
						t.Fatalf("'want' directive needs argument <<%s>>", line)
					}
					current.want[words[1]] = kind[0] != '!'
				default:
					t.Fatalf("%s: invalid directive %q", filename, kind)
				}
			}

			for _, tc := range cases {
				t.Run(fmt.Sprintf("L%d", tc.linenum), func(t *testing.T) {
					// Run the command.
					cmd := exec.Command(exe, tc.args...)
					cmd.Stdout = new(bytes.Buffer)
					cmd.Stderr = new(bytes.Buffer)
					cmd.Dir = tmpdir
					cmd.Env = append(os.Environ(), "GOPROXY=", "GO111MODULE=on")
					var got string
					if err := cmd.Run(); err != nil {
						if !tc.wantErr {
							t.Fatalf("deadcode failed: %v (stderr=%s)", err, cmd.Stderr)
						}
						got = fmt.Sprint(cmd.Stderr)
					} else {
						if tc.wantErr {
							t.Fatalf("deadcode succeeded unexpectedly (stdout=%s)", cmd.Stdout)
						}
						got = fmt.Sprint(cmd.Stdout)
					}

					// Check each want directive.
					for str, sense := range tc.want {
						ok := true
						if strings.Contains(got, str) != sense {
							if sense {
								t.Errorf("missing %q", str)
							} else {
								t.Errorf("unwanted %q", str)
							}
							ok = false
						}
						if !ok {
							t.Errorf("got: <<%s>>", got)
						}
					}
				})
			}
		})
	}
}

// TestDeterminism checks that the output of the deadcode command
// does not vary from one run to the next. It has a different form
// from the other tests because it is intentionally order-sensitive.
func TestDeterminism(t *testing.T) {
	testenv.NeedsTool(t, "go")

	const archive = `
-- go.mod --
module example.com
go 1.21

-- main.go --
package main

// These dead functions are all declared on the same line.
func dead1() {}; func dead2() {}; func dead3() {}; func dead4() {}; func dead5() {}; func dead6() {}

// These live functions all have the same signature and are
// address-taken, so they are all candidate callees of the dynamic
// call in main, and all of them reach target by a path of the same
// length. (There is no static path to target.)
var funcs = []func(){live1, live2, live3, live4, live5, live6}

func main() {
	for _, f := range funcs {
		f()
	}
}

func live1() { target() }
func live2() { target() }
func live3() { target() }
func live4() { target() }
func live5() { target() }
func live6() { target() }

func target() {}
`
	exe := buildDeadcode(t)
	tmpdir := extractTxtar(t, txtar.Parse([]byte(archive)))

	for _, args := range [][]string{
		{"."},
		{"-whylive=example.com.target", "."},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			t.Parallel()

			// A single discrepancy is enough to fail.
			// (Before the fix, both scenarios were detected
			// within the first three runs.)
			const runs = 10
			var first string
			for i := range runs {
				cmd := exec.Command(exe, args...)
				cmd.Stdout = new(bytes.Buffer)
				cmd.Stderr = new(bytes.Buffer)
				cmd.Dir = tmpdir
				cmd.Env = append(os.Environ(), "GOPROXY=", "GO111MODULE=on")
				if err := cmd.Run(); err != nil {
					t.Fatalf("deadcode failed: %v (stderr=%s)", err, cmd.Stderr)
				}
				got := fmt.Sprint(cmd.Stdout)
				if i == 0 {
					first = got
				} else if got != first {
					t.Fatalf("output of run #%d differs from run #1:\n"+
						"run #1: <<%s>>\nrun #%d: <<%s>>", i+1, first, i+1, got)
				}
			}
		})
	}
}

// extractTxtar writes the files of the archive to a new
// temporary directory and returns its name.
//
// TODO(adonovan): move to [testfiles.ExtractTxtarToTmp].
// Many other tests want it.
func extractTxtar(t *testing.T, ar *txtar.Archive) string {
	tmpdir := t.TempDir()
	for _, f := range ar.Files {
		filename := filepath.Join(tmpdir, f.Name)
		if err := os.MkdirAll(filepath.Dir(filename), 0777); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filename, f.Data, 0666); err != nil {
			t.Fatal(err)
		}
	}
	return tmpdir
}

// buildDeadcode builds the deadcode executable.
// It returns its path, and a cleanup function.
func buildDeadcode(t *testing.T) string {
	bin := filepath.Join(t.TempDir(), "deadcode")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	cmd := exec.Command("go", "build", "-o", bin)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("Building deadcode: %v\n%s", err, out)
	}
	return bin
}

// words breaks a string into words, respecting
// Go string quotations around words with spaces.
func words(s string) ([]string, error) {
	var words []string
	for s != "" {
		s = strings.TrimSpace(s)
		var word string
		if s[0] == '"' || s[0] == '`' {
			prefix, err := strconv.QuotedPrefix(s)
			if err != nil {
				return nil, err
			}
			s = s[len(prefix):]
			word, _ = strconv.Unquote(prefix)
		} else {
			prefix, rest, _ := strings.Cut(s, " ")
			s = rest
			word = prefix
		}
		words = append(words, word)
	}
	return words, nil
}
