// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package workspace

import (
	"sort"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/gopls/internal/protocol/command"
	. "golang.org/x/tools/gopls/internal/test/integration"
)

func TestPackages(t *testing.T) {
	const files = `
-- go.mod --
module foo

-- foo.go --
package foo
func Foo()

-- bar/bar.go --
package bar
func Bar()

-- baz/go.mod --
module baz

-- baz/baz.go --
package baz
func Baz()
`

	t.Run("file", func(t *testing.T) {
		Run(t, files, func(t *testing.T, env *Env) {
			checkPackages(t, env, []protocol.DocumentURI{env.Editor.DocumentURI("foo.go")}, false, 0, []command.Package{
				{
					Path:       "foo",
					ModulePath: "foo",
				},
			}, map[string]command.Module{
				"foo": {
					Path:  "foo",
					GoMod: env.Editor.DocumentURI("go.mod"),
				},
			}, []string{})
		})
	})

	t.Run("package", func(t *testing.T) {
		Run(t, files, func(t *testing.T, env *Env) {
			checkPackages(t, env, []protocol.DocumentURI{env.Editor.DocumentURI("bar")}, false, 0, []command.Package{
				{
					Path:       "foo/bar",
					ModulePath: "foo",
				},
			}, map[string]command.Module{
				"foo": {
					Path:  "foo",
					GoMod: env.Editor.DocumentURI("go.mod"),
				},
			}, []string{})
		})
	})

	t.Run("workspace", func(t *testing.T) {
		Run(t, files, func(t *testing.T, env *Env) {
			checkPackages(t, env, []protocol.DocumentURI{env.Editor.DocumentURI("")}, true, 0, []command.Package{
				{
					Path:       "foo",
					ModulePath: "foo",
				},
				{
					Path:       "foo/bar",
					ModulePath: "foo",
				},
			}, map[string]command.Module{
				"foo": {
					Path:  "foo",
					GoMod: env.Editor.DocumentURI("go.mod"),
				},
			}, []string{})
		})
	})

	t.Run("nested module", func(t *testing.T) {
		Run(t, files, func(t *testing.T, env *Env) {
			// Load the nested module
			env.OpenFile("baz/baz.go")

			// Request packages using the URI of the nested module _directory_
			checkPackages(t, env, []protocol.DocumentURI{env.Editor.DocumentURI("baz")}, true, 0, []command.Package{
				{
					Path:       "baz",
					ModulePath: "baz",
				},
			}, map[string]command.Module{
				"baz": {
					Path:  "baz",
					GoMod: env.Editor.DocumentURI("baz/go.mod"),
				},
			}, []string{})
		})
	})
}

func TestPackagesWithTests(t *testing.T) {
	const files = `
-- go.mod --
module foo

-- foo.go --
package foo
import "testing"
func Foo()
func TestFoo2(t *testing.T)
func foo()

-- foo_test.go --
package foo
import "testing"
func TestFoo(t *testing.T)
func Issue70927(*error)
func Test_foo(t *testing.T)

-- foo2_test.go --
package foo_test
import "testing"
func TestBar(t *testing.T) {}

-- baz/baz_test.go --
package baz
import "testing"
func TestBaz(*testing.T)
func BenchmarkBaz(*testing.B)
func FuzzBaz(*testing.F)
func ExampleBaz()

-- bat/go.mod --
module bat

-- bat/bat_test.go --
package bat
import "testing"
func Test(*testing.T)
`

	t.Run("file", func(t *testing.T) {
		Run(t, files, func(t *testing.T, env *Env) {
			checkPackages(t, env, []protocol.DocumentURI{env.Editor.DocumentURI("foo_test.go")}, false, command.NeedTests, []command.Package{
				{
					Path:       "foo",
					ModulePath: "foo",
				},
				{
					Path:       "foo",
					ForTest:    "foo",
					ModulePath: "foo",
					TestFiles: []command.TestFile{
						{
							URI: env.Editor.DocumentURI("foo_test.go"),
							Tests: []command.TestCase{
								{Name: "TestFoo"},
								{Name: "Test_foo"},
							},
						},
					},
				},
				{
					Path:       "foo_test",
					ForTest:    "foo",
					ModulePath: "foo",
					TestFiles: []command.TestFile{
						{
							URI: env.Editor.DocumentURI("foo2_test.go"),
							Tests: []command.TestCase{
								{Name: "TestBar"},
							},
						},
					},
				},
			}, map[string]command.Module{
				"foo": {
					Path:  "foo",
					GoMod: env.Editor.DocumentURI("go.mod"),
				},
			}, []string{
				"func TestFoo(t *testing.T)",
				"func Test_foo(t *testing.T)",
				"func TestBar(t *testing.T) {}",
			})
		})
	})

	t.Run("package", func(t *testing.T) {
		Run(t, files, func(t *testing.T, env *Env) {
			checkPackages(t, env, []protocol.DocumentURI{env.Editor.DocumentURI("baz")}, false, command.NeedTests, []command.Package{
				{
					Path:       "foo/baz",
					ForTest:    "foo/baz",
					ModulePath: "foo",
					TestFiles: []command.TestFile{
						{
							URI: env.Editor.DocumentURI("baz/baz_test.go"),
							Tests: []command.TestCase{
								{Name: "TestBaz"},
								{Name: "BenchmarkBaz"},
								{Name: "FuzzBaz"},
								{Name: "ExampleBaz"},
							},
						},
					},
				},
			}, map[string]command.Module{
				"foo": {
					Path:  "foo",
					GoMod: env.Editor.DocumentURI("go.mod"),
				},
			}, []string{
				"func TestBaz(*testing.T)",
				"func BenchmarkBaz(*testing.B)",
				"func FuzzBaz(*testing.F)",
				"func ExampleBaz()",
			})
		})
	})

	t.Run("workspace", func(t *testing.T) {
		Run(t, files, func(t *testing.T, env *Env) {
			checkPackages(t, env, []protocol.DocumentURI{env.Editor.DocumentURI(".")}, true, command.NeedTests, []command.Package{
				{
					Path:       "foo",
					ModulePath: "foo",
				},
				{
					Path:       "foo",
					ForTest:    "foo",
					ModulePath: "foo",
					TestFiles: []command.TestFile{
						{
							URI: env.Editor.DocumentURI("foo_test.go"),
							Tests: []command.TestCase{
								{Name: "TestFoo"},
								{Name: "Test_foo"},
							},
						},
					},
				},
				{
					Path:       "foo/baz",
					ForTest:    "foo/baz",
					ModulePath: "foo",
					TestFiles: []command.TestFile{
						{
							URI: env.Editor.DocumentURI("baz/baz_test.go"),
							Tests: []command.TestCase{
								{Name: "TestBaz"},
								{Name: "BenchmarkBaz"},
								{Name: "FuzzBaz"},
								{Name: "ExampleBaz"},
							},
						},
					},
				},
				{
					Path:       "foo_test",
					ForTest:    "foo",
					ModulePath: "foo",
					TestFiles: []command.TestFile{
						{
							URI: env.Editor.DocumentURI("foo2_test.go"),
							Tests: []command.TestCase{
								{Name: "TestBar"},
							},
						},
					},
				},
			}, map[string]command.Module{
				"foo": {
					Path:  "foo",
					GoMod: env.Editor.DocumentURI("go.mod"),
				},
			}, []string{
				"func TestFoo(t *testing.T)",
				"func Test_foo(t *testing.T)",
				"func TestBaz(*testing.T)",
				"func BenchmarkBaz(*testing.B)",
				"func FuzzBaz(*testing.F)",
				"func ExampleBaz()",
				"func TestBar(t *testing.T) {}",
			})
		})
	})

	t.Run("nested module", func(t *testing.T) {
		Run(t, files, func(t *testing.T, env *Env) {
			// Load the nested module
			env.OpenFile("bat/bat_test.go")

			// Request packages using the URI of the nested module _directory_
			checkPackages(t, env, []protocol.DocumentURI{env.Editor.DocumentURI("bat")}, true, command.NeedTests, []command.Package{
				{
					Path:       "bat",
					ForTest:    "bat",
					ModulePath: "bat",
					TestFiles: []command.TestFile{
						{
							URI: env.Editor.DocumentURI("bat/bat_test.go"),
							Tests: []command.TestCase{
								{Name: "Test"},
							},
						},
					},
				},
			}, map[string]command.Module{
				"bat": {
					Path:  "bat",
					GoMod: env.Editor.DocumentURI("bat/go.mod"),
				},
			}, []string{
				"func Test(*testing.T)",
			})
		})
	})
}

func TestPackagesWithSubtests(t *testing.T) {
	const files = `
-- go.mod --
module foo

-- foo_test.go --
package foo

import "testing"

// Verify that examples don't break subtest detection
func ExampleFoo() {}

func TestFoo(t *testing.T) {
	t.Run("Bar", func(t *testing.T) {
		t.Run("Baz", func(t *testing.T) {})
	})
	t.Run("Bar", func(t *testing.T) {})
	t.Run("Bar", func(t *testing.T) {})
	t.Run("with space", func(t *testing.T) {})

	var x X
	y := func(t *testing.T) {
		t.Run("VarSub", func(t *testing.T) {})
	}
	t.Run("SubtestFunc", SubtestFunc)
	t.Run("SubtestMethod", x.SubtestMethod)
	t.Run("SubtestVar", y)
}

func SubtestFunc(t *testing.T) {
	t.Run("FuncSub", func(t *testing.T) {})
}

type X int
func (X) SubtestMethod(t *testing.T) {
	t.Run("MethodSub", func(t *testing.T) {})
}
`

	Run(t, files, func(t *testing.T, env *Env) {
		checkPackages(t, env, []protocol.DocumentURI{env.Editor.DocumentURI("foo_test.go")}, false, command.NeedTests, []command.Package{
			{
				Path:       "foo",
				ForTest:    "foo",
				ModulePath: "foo",
				TestFiles: []command.TestFile{
					{
						URI: env.Editor.DocumentURI("foo_test.go"),
						Tests: []command.TestCase{
							{Name: "ExampleFoo"},
							{Name: "TestFoo"},
							{Name: "TestFoo/Bar"},
							{Name: "TestFoo/Bar/Baz"},
							{Name: "TestFoo/Bar#01"},
							{Name: "TestFoo/Bar#02"},
							{Name: "TestFoo/with_space"},
							{Name: "TestFoo/SubtestFunc"},
							{Name: "TestFoo/SubtestFunc/FuncSub"},
							{Name: "TestFoo/SubtestMethod"},
							{Name: "TestFoo/SubtestMethod/MethodSub"},
							{Name: "TestFoo/SubtestVar"},
							// {Name: "TestFoo/SubtestVar/VarSub"}, // TODO
						},
					},
				},
			},
		}, map[string]command.Module{
			"foo": {
				Path:  "foo",
				GoMod: env.Editor.DocumentURI("go.mod"),
			},
		}, []string{
			"func ExampleFoo() {}",
			`func TestFoo(t *testing.T) {
	t.Run("Bar", func(t *testing.T) {
		t.Run("Baz", func(t *testing.T) {})
	})
	t.Run("Bar", func(t *testing.T) {})
	t.Run("Bar", func(t *testing.T) {})
	t.Run("with space", func(t *testing.T) {})

	var x X
	y := func(t *testing.T) {
		t.Run("VarSub", func(t *testing.T) {})
	}
	t.Run("SubtestFunc", SubtestFunc)
	t.Run("SubtestMethod", x.SubtestMethod)
	t.Run("SubtestVar", y)
}`,
			"t.Run(\"Bar\", func(t *testing.T) {\n\t\tt.Run(\"Baz\", func(t *testing.T) {})\n\t})",
			`t.Run("Baz", func(t *testing.T) {})`,
			`t.Run("Bar", func(t *testing.T) {})`,
			`t.Run("Bar", func(t *testing.T) {})`,
			`t.Run("with space", func(t *testing.T) {})`,
			`t.Run("SubtestFunc", SubtestFunc)`,
			`t.Run("FuncSub", func(t *testing.T) {})`,
			`t.Run("SubtestMethod", x.SubtestMethod)`,
			`t.Run("MethodSub", func(t *testing.T) {})`,
			`t.Run("SubtestVar", y)`,
		})
	})
}

func checkPackages(t testing.TB, env *Env, files []protocol.DocumentURI, recursive bool, mode command.PackagesMode, wantPkg []command.Package, wantModule map[string]command.Module, wantSource []string) {
	t.Helper()

	cmd := command.NewPackagesCommand("Packages", command.PackagesArgs{Files: files, Recursive: recursive, Mode: mode})
	var result command.PackagesResult
	env.ExecuteCommand(&protocol.ExecuteCommandParams{
		Command:   command.Packages.String(),
		Arguments: cmd.Arguments,
	}, &result)

	// The ordering of packages is undefined so sort the results to ensure
	// consistency
	sort.Slice(result.Packages, func(i, j int) bool {
		a, b := result.Packages[i], result.Packages[j]
		c := strings.Compare(a.Path, b.Path)
		if c != 0 {
			return c < 0
		}
		return strings.Compare(a.ForTest, b.ForTest) < 0
	})

	// Instead of testing the exact values of the test locations (which would
	// make these tests significantly more trouble to maintain), verify the
	// source range they refer to.
	gotSource := []string{} // avoid issues with comparing null to []
	for i := range result.Packages {
		pkg := &result.Packages[i]
		for i := range pkg.TestFiles {
			file := &pkg.TestFiles[i]
			env.OpenFile(file.URI.Path())

			for i := range file.Tests {
				test := &file.Tests[i]
				gotSource = append(gotSource, env.FileContentAt(test.Loc))
				test.Loc = protocol.Location{}
			}
		}
	}

	if diff := cmp.Diff(wantPkg, result.Packages); diff != "" {
		t.Errorf("Packages(%v) returned unexpected packages (-want +got):\n%s", files, diff)
	}

	if diff := cmp.Diff(wantModule, result.Module); diff != "" {
		t.Errorf("Packages(%v) returned unexpected modules (-want +got):\n%s", files, diff)
	}

	// Don't check the source if the response is incorrect
	if !t.Failed() {
		if diff := cmp.Diff(wantSource, gotSource); diff != "" {
			t.Errorf("Packages(%v) returned unexpected test case ranges (-want +got):\n%s", files, diff)
		}
	}
}
