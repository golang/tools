// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package regtest

import (
	"testing"

	"golang.org/x/tools/gopls/internal/hooks"
	. "golang.org/x/tools/internal/lsp/regtest"

	"golang.org/x/tools/internal/lsp/fake"
	"golang.org/x/tools/internal/lsp/protocol"
	"golang.org/x/tools/internal/testenv"
)

func TestMain(m *testing.M) {
	Main(m, hooks.Options)
}

func TestEditFile(t *testing.T) {
	const pkg = `
-- go.mod --
module mod.com

go 1.14
-- a/a.go --
package a

func _() {
	var x int
}
`
	// Edit the file when it's *not open* in the workspace, and check that
	// diagnostics are updated.
	t.Run("unopened", func(t *testing.T) {
		Run(t, pkg, func(t *testing.T, env *Env) {
			env.Await(
				env.DiagnosticAtRegexp("a/a.go", "x"),
			)
			env.WriteWorkspaceFile("a/a.go", `package a; func _() {};`)
			env.Await(
				EmptyDiagnostics("a/a.go"),
			)
		})
	})

	// Edit the file when it *is open* in the workspace, and check that
	// diagnostics are *not* updated.
	t.Run("opened", func(t *testing.T) {
		Run(t, pkg, func(t *testing.T, env *Env) {
			env.OpenFile("a/a.go")
			// Insert a trivial edit so that we don't automatically update the buffer
			// (see CL 267577).
			env.EditBuffer("a/a.go", fake.NewEdit(0, 0, 0, 0, " "))
			env.Await(env.DoneWithOpen())
			env.WriteWorkspaceFile("a/a.go", `package a; func _() {};`)
			env.Await(
				OnceMet(
					env.DoneWithChangeWatchedFiles(),
					env.DiagnosticAtRegexp("a/a.go", "x"),
				))
		})
	})
}

// Edit a dependency on disk and expect a new diagnostic.
func TestEditDependency(t *testing.T) {
	const pkg = `
-- go.mod --
module mod.com

go 1.14
-- b/b.go --
package b

func B() int { return 0 }
-- a/a.go --
package a

import (
	"mod.com/b"
)

func _() {
	_ = b.B()
}
`
	Run(t, pkg, func(t *testing.T, env *Env) {
		env.OpenFile("a/a.go")
		env.Await(env.DoneWithOpen())
		env.WriteWorkspaceFile("b/b.go", `package b; func B() {};`)
		env.Await(
			env.DiagnosticAtRegexp("a/a.go", "b.B"),
		)
	})
}

// Edit both the current file and one of its dependencies on disk and
// expect diagnostic changes.
func TestEditFileAndDependency(t *testing.T) {
	const pkg = `
-- go.mod --
module mod.com

go 1.14
-- b/b.go --
package b

func B() int { return 0 }
-- a/a.go --
package a

import (
	"mod.com/b"
)

func _() {
	var x int
	_ = b.B()
}
`
	Run(t, pkg, func(t *testing.T, env *Env) {
		env.Await(
			env.DiagnosticAtRegexp("a/a.go", "x"),
		)
		env.WriteWorkspaceFiles(map[string]string{
			"b/b.go": `package b; func B() {};`,
			"a/a.go": `package a

import "mod.com/b"

func _() {
	b.B()
}`,
		})
		env.Await(
			EmptyDiagnostics("a/a.go"),
			NoDiagnostics("b/b.go"),
		)
	})
}

// Delete a dependency and expect a new diagnostic.
func TestDeleteDependency(t *testing.T) {
	const pkg = `
-- go.mod --
module mod.com

go 1.14
-- b/b.go --
package b

func B() int { return 0 }
-- a/a.go --
package a

import (
	"mod.com/b"
)

func _() {
	_ = b.B()
}
`
	Run(t, pkg, func(t *testing.T, env *Env) {
		env.OpenFile("a/a.go")
		env.Await(env.DoneWithOpen())
		env.RemoveWorkspaceFile("b/b.go")
		env.Await(
			env.DiagnosticAtRegexp("a/a.go", "\"mod.com/b\""),
		)
	})
}

// Create a dependency on disk and expect the diagnostic to go away.
func TestCreateDependency(t *testing.T) {
	const missing = `
-- go.mod --
module mod.com

go 1.14
-- b/b.go --
package b

func B() int { return 0 }
-- a/a.go --
package a

import (
	"mod.com/c"
)

func _() {
	c.C()
}
`
	Run(t, missing, func(t *testing.T, env *Env) {
		t.Skip("the initial workspace load fails and never retries")

		env.Await(
			env.DiagnosticAtRegexp("a/a.go", "\"mod.com/c\""),
		)
		env.WriteWorkspaceFile("c/c.go", `package c; func C() {};`)
		env.Await(
			EmptyDiagnostics("c/c.go"),
		)
	})
}

// Create a new dependency and add it to the file on disk.
// This is similar to what might happen if you switch branches.
func TestCreateAndAddDependency(t *testing.T) {
	const original = `
-- go.mod --
module mod.com

go 1.14
-- a/a.go --
package a

func _() {}
`
	Run(t, original, func(t *testing.T, env *Env) {
		env.WriteWorkspaceFile("c/c.go", `package c; func C() {};`)
		env.WriteWorkspaceFile("a/a.go", `package a; import "mod.com/c"; func _() { c.C() }`)
		env.Await(
			NoDiagnostics("a/a.go"),
		)
	})
}

// Create a new file that defines a new symbol, in the same package.
func TestCreateFile(t *testing.T) {
	const pkg = `
-- go.mod --
module mod.com

go 1.14
-- a/a.go --
package a

func _() {
	hello()
}
`
	Run(t, pkg, func(t *testing.T, env *Env) {
		env.Await(
			env.DiagnosticAtRegexp("a/a.go", "hello"),
		)
		env.WriteWorkspaceFile("a/a2.go", `package a; func hello() {};`)
		env.Await(
			EmptyDiagnostics("a/a.go"),
		)
	})
}

// Add a new method to an interface and implement it.
// Inspired by the structure of internal/lsp/source and internal/lsp/cache.
func TestCreateImplementation(t *testing.T) {
	const pkg = `
-- go.mod --
module mod.com

go 1.14
-- b/b.go --
package b

type B interface{
	Hello() string
}

func SayHello(bee B) {
	println(bee.Hello())
}
-- a/a.go --
package a

import "mod.com/b"

type X struct {}

func (_ X) Hello() string {
	return ""
}

func _() {
	x := X{}
	b.SayHello(x)
}
`
	const newMethod = `package b
type B interface{
	Hello() string
	Bye() string
}

func SayHello(bee B) {
	println(bee.Hello())
}`
	const implementation = `package a

import "mod.com/b"

type X struct {}

func (_ X) Hello() string {
	return ""
}

func (_ X) Bye() string {
	return ""
}

func _() {
	x := X{}
	b.SayHello(x)
}`

	// Add the new method before the implementation. Expect diagnostics.
	t.Run("method before implementation", func(t *testing.T) {
		Run(t, pkg, func(t *testing.T, env *Env) {
			env.WriteWorkspaceFile("b/b.go", newMethod)
			env.Await(
				OnceMet(
					env.DoneWithChangeWatchedFiles(),
					DiagnosticAt("a/a.go", 12, 12),
				),
			)
			env.WriteWorkspaceFile("a/a.go", implementation)
			env.Await(
				EmptyDiagnostics("a/a.go"),
			)
		})
	})
	// Add the new implementation before the new method. Expect no diagnostics.
	t.Run("implementation before method", func(t *testing.T) {
		Run(t, pkg, func(t *testing.T, env *Env) {
			env.WriteWorkspaceFile("a/a.go", implementation)
			env.Await(
				OnceMet(
					env.DoneWithChangeWatchedFiles(),
					NoDiagnostics("a/a.go"),
				),
			)
			env.WriteWorkspaceFile("b/b.go", newMethod)
			env.Await(
				NoDiagnostics("a/a.go"),
			)
		})
	})
	// Add both simultaneously. Expect no diagnostics.
	t.Run("implementation and method simultaneously", func(t *testing.T) {
		Run(t, pkg, func(t *testing.T, env *Env) {
			env.WriteWorkspaceFiles(map[string]string{
				"a/a.go": implementation,
				"b/b.go": newMethod,
			})
			env.Await(
				OnceMet(
					env.DoneWithChangeWatchedFiles(),
					NoDiagnostics("a/a.go"),
				),
				NoDiagnostics("b/b.go"),
			)
		})
	})
}

// Tests golang/go#38498. Delete a file and then force a reload.
// Assert that we no longer try to load the file.
func TestDeleteFiles(t *testing.T) {
	testenv.NeedsGo1Point(t, 13) // Poor overlay support causes problems on 1.12.
	const pkg = `
-- go.mod --
module mod.com

go 1.14
-- a/a.go --
package a

func _() {
	var _ int
}
-- a/a_unneeded.go --
package a
`
	t.Run("close then delete", func(t *testing.T) {
		WithOptions(EditorConfig{
			VerboseOutput: true,
		}).Run(t, pkg, func(t *testing.T, env *Env) {
			env.OpenFile("a/a.go")
			env.OpenFile("a/a_unneeded.go")
			env.Await(
				OnceMet(
					env.DoneWithOpen(),
					LogMatching(protocol.Info, "a_unneeded.go", 1),
				),
			)

			// Close and delete the open file, mimicking what an editor would do.
			env.CloseBuffer("a/a_unneeded.go")
			env.RemoveWorkspaceFile("a/a_unneeded.go")
			env.RegexpReplace("a/a.go", "var _ int", "fmt.Println(\"\")")
			env.Await(
				env.DiagnosticAtRegexp("a/a.go", "fmt"),
			)
			env.SaveBuffer("a/a.go")
			env.Await(
				OnceMet(
					env.DoneWithSave(),
					// There should only be one log message containing
					// a_unneeded.go, from the initial workspace load, which we
					// check for earlier. If there are more, there's a bug.
					LogMatching(protocol.Info, "a_unneeded.go", 1),
				),
				EmptyDiagnostics("a/a.go"),
			)
		})
	})

	t.Run("delete then close", func(t *testing.T) {
		WithOptions(
			EditorConfig{VerboseOutput: true},
		).Run(t, pkg, func(t *testing.T, env *Env) {
			env.OpenFile("a/a.go")
			env.OpenFile("a/a_unneeded.go")
			env.Await(
				OnceMet(
					env.DoneWithOpen(),
					LogMatching(protocol.Info, "a_unneeded.go", 1),
				),
			)

			// Delete and then close the file.
			env.RemoveWorkspaceFile("a/a_unneeded.go")
			env.CloseBuffer("a/a_unneeded.go")
			env.RegexpReplace("a/a.go", "var _ int", "fmt.Println(\"\")")
			env.Await(
				env.DiagnosticAtRegexp("a/a.go", "fmt"),
			)
			env.SaveBuffer("a/a.go")
			env.Await(
				OnceMet(
					env.DoneWithSave(),
					// There should only be one log message containing
					// a_unneeded.go, from the initial workspace load, which we
					// check for earlier. If there are more, there's a bug.
					LogMatching(protocol.Info, "a_unneeded.go", 1),
				),
				EmptyDiagnostics("a/a.go"),
			)
		})
	})
}

// This change reproduces the behavior of switching branches, with multiple
// files being created and deleted. The key change here is the movement of a
// symbol from one file to another in a given package through a deletion and
// creation. To reproduce an issue with metadata invalidation in batched
// changes, the last change in the batch is an on-disk file change that doesn't
// require metadata invalidation.
func TestMoveSymbol(t *testing.T) {
	const pkg = `
-- go.mod --
module mod.com

go 1.14
-- main.go --
package main

import "mod.com/a"

func main() {
	var x int
	x = a.Hello
	println(x)
}
-- a/a1.go --
package a

var Hello int
-- a/a2.go --
package a

func _() {}
`
	Run(t, pkg, func(t *testing.T, env *Env) {
		env.ChangeFilesOnDisk([]fake.FileEvent{
			{
				Path: "a/a3.go",
				Content: `package a

var Hello int
`,
				ProtocolEvent: protocol.FileEvent{
					URI:  env.Sandbox.Workdir.URI("a/a3.go"),
					Type: protocol.Created,
				},
			},
			{
				Path: "a/a1.go",
				ProtocolEvent: protocol.FileEvent{
					URI:  env.Sandbox.Workdir.URI("a/a1.go"),
					Type: protocol.Deleted,
				},
			},
			{
				Path:    "a/a2.go",
				Content: `package a; func _() {};`,
				ProtocolEvent: protocol.FileEvent{
					URI:  env.Sandbox.Workdir.URI("a/a2.go"),
					Type: protocol.Changed,
				},
			},
		})
		env.Await(
			OnceMet(
				env.DoneWithChangeWatchedFiles(),
				NoDiagnostics("main.go"),
			),
		)
	})
}

// Reproduce golang/go#40456.
func TestChangeVersion(t *testing.T) {
	const proxy = `
-- example.com@v1.2.3/go.mod --
module example.com

go 1.12
-- example.com@v1.2.3/blah/blah.go --
package blah

const Name = "Blah"

func X(x int) {}
-- example.com@v1.2.2/go.mod --
module example.com

go 1.12
-- example.com@v1.2.2/blah/blah.go --
package blah

const Name = "Blah"

func X() {}
-- random.org@v1.2.3/go.mod --
module random.org

go 1.12
-- random.org@v1.2.3/blah/blah.go --
package hello

const Name = "Hello"
`
	const mod = `
-- go.mod --
module mod.com

go 1.12

require example.com v1.2.2
-- main.go --
package main

import "example.com/blah"

func main() {
	blah.X()
}
`
	WithOptions(ProxyFiles(proxy)).Run(t, mod, func(t *testing.T, env *Env) {
		env.WriteWorkspaceFiles(map[string]string{
			"go.mod": `module mod.com

go 1.12

require example.com v1.2.3
`,
			"main.go": `package main

import (
	"example.com/blah"
)

func main() {
	blah.X(1)
}
`,
		})
		env.Await(
			env.DoneWithChangeWatchedFiles(),
			NoDiagnostics("main.go"),
		)
	})
}

// Reproduces golang/go#40340.
func TestSwitchFromGOPATHToModules(t *testing.T) {
	testenv.NeedsGo1Point(t, 13)

	const files = `
-- foo/blah/blah.go --
package blah

const Name = ""
-- foo/main.go --
package main

import "blah"

func main() {
	_ = blah.Name
}
`
	WithOptions(
		InGOPATH(),
		EditorConfig{
			Env: map[string]string{
				"GO111MODULE": "auto",
			},
		},
		Modes(Experimental), // module is in a subdirectory
	).Run(t, files, func(t *testing.T, env *Env) {
		env.OpenFile("foo/main.go")
		env.Await(env.DiagnosticAtRegexp("foo/main.go", `"blah"`))
		if err := env.Sandbox.RunGoCommand(env.Ctx, "foo", "mod", []string{"init", "mod.com"}); err != nil {
			t.Fatal(err)
		}
		env.RegexpReplace("foo/main.go", `"blah"`, `"mod.com/blah"`)
		env.Await(
			EmptyDiagnostics("foo/main.go"),
		)
	})
}

// Reproduces golang/go#40487.
func TestSwitchFromModulesToGOPATH(t *testing.T) {
	testenv.NeedsGo1Point(t, 13)

	const files = `
-- foo/go.mod --
module mod.com

go 1.14
-- foo/blah/blah.go --
package blah

const Name = ""
-- foo/main.go --
package main

import "mod.com/blah"

func main() {
	_ = blah.Name
}
`
	WithOptions(
		InGOPATH(),
		EditorConfig{
			Env: map[string]string{
				"GO111MODULE": "auto",
			},
		},
	).Run(t, files, func(t *testing.T, env *Env) {
		env.OpenFile("foo/main.go")
		env.RemoveWorkspaceFile("foo/go.mod")
		env.Await(
			OnceMet(
				env.DoneWithChangeWatchedFiles(),
				env.DiagnosticAtRegexp("foo/main.go", `"mod.com/blah"`),
			),
		)
		env.RegexpReplace("foo/main.go", `"mod.com/blah"`, `"foo/blah"`)
		env.Await(
			EmptyDiagnostics("foo/main.go"),
		)
	})
}

func TestNewSymbolInTestVariant(t *testing.T) {
	const files = `
-- go.mod --
module mod.com

go 1.12
-- a/a.go --
package a

func bob() {}
-- a/a_test.go --
package a

import "testing"

func TestBob(t *testing.T) {
	bob()
}
`
	Run(t, files, func(t *testing.T, env *Env) {
		// Add a new symbol to the package under test and use it in the test
		// variant. Expect no diagnostics.
		env.WriteWorkspaceFiles(map[string]string{
			"a/a.go": `package a

func bob() {}
func george() {}
`,
			"a/a_test.go": `package a

import "testing"

func TestAll(t *testing.T) {
	bob()
	george()
}
`,
		})
		env.Await(
			OnceMet(
				env.DoneWithChangeWatchedFiles(),
				NoDiagnostics("a/a.go"),
			),
			OnceMet(
				env.DoneWithChangeWatchedFiles(),
				NoDiagnostics("a/a_test.go"),
			),
		)
		// Now, add a new file to the test variant and use its symbol in the
		// original test file. Expect no diagnostics.
		env.WriteWorkspaceFiles(map[string]string{
			"a/a_test.go": `package a

import "testing"

func TestAll(t *testing.T) {
	bob()
	george()
	hi()
}
`,
			"a/a2_test.go": `package a

import "testing"

func hi() {}

func TestSomething(t *testing.T) {}
`,
		})
		env.Await(
			OnceMet(
				env.DoneWithChangeWatchedFiles(),
				NoDiagnostics("a/a_test.go"),
			),
			OnceMet(
				env.DoneWithChangeWatchedFiles(),
				NoDiagnostics("a/a2_test.go"),
			),
		)
	})
}
