// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package watch

import (
	"os"
	"testing"
	"time"

	. "golang.org/x/tools/gopls/internal/test/integration"
	"golang.org/x/tools/gopls/internal/util/bug"

	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/gopls/internal/server"
	"golang.org/x/tools/gopls/internal/test/integration/fake"
)

func TestMain(m *testing.M) {
	bug.PanicOnBugs = true
	os.Exit(Main(m))
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
			env.OnceMet(
				InitialWorkspaceLoad,
				Diagnostics(env.AtRegexp("a/a.go", "x")),
			)
			env.WriteWorkspaceFile("a/a.go", `package a; func _() {};`)
			env.AfterChange(
				NoDiagnostics(ForFile("a/a.go")),
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
			env.AfterChange()
			env.WriteWorkspaceFile("a/a.go", `package a; func _() {};`)
			env.AfterChange(
				Diagnostics(env.AtRegexp("a/a.go", "x")),
			)
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
		env.AfterChange()
		env.WriteWorkspaceFile("b/b.go", `package b; func B() {};`)
		env.AfterChange(
			Diagnostics(env.AtRegexp("a/a.go", "b.B")),
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
		env.OnceMet(
			InitialWorkspaceLoad,
			Diagnostics(env.AtRegexp("a/a.go", "x")),
		)
		env.WriteWorkspaceFiles(map[string]string{
			"b/b.go": `package b; func B() {};`,
			"a/a.go": `package a

import "mod.com/b"

func _() {
	b.B()
}`,
		})
		env.AfterChange(
			NoDiagnostics(ForFile("a/a.go")),
			NoDiagnostics(ForFile("b/b.go")),
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
		env.AfterChange()
		env.RemoveWorkspaceFile("b/b.go")
		env.AfterChange(
			Diagnostics(env.AtRegexp("a/a.go", "\"mod.com/b\"")),
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
		env.OnceMet(
			InitialWorkspaceLoad,
			Diagnostics(env.AtRegexp("a/a.go", "\"mod.com/c\"")),
		)
		env.WriteWorkspaceFile("c/c.go", `package c; func C() {};`)
		env.AfterChange(
			NoDiagnostics(ForFile("a/a.go")),
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
		env.AfterChange(
			NoDiagnostics(ForFile("a/a.go")),
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
		env.OnceMet(
			InitialWorkspaceLoad,
			Diagnostics(env.AtRegexp("a/a.go", "hello")),
		)
		env.WriteWorkspaceFile("a/a2.go", `package a; func hello() {};`)
		env.AfterChange(
			NoDiagnostics(ForFile("a/a.go")),
		)
	})
}

// Add a new method to an interface and implement it.
// Inspired by the structure of internal/golang and internal/cache.
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
			env.AfterChange(
				Diagnostics(AtPosition("a/a.go", 12, 12)),
			)
			env.WriteWorkspaceFile("a/a.go", implementation)
			env.AfterChange(
				NoDiagnostics(ForFile("a/a.go")),
			)
		})
	})
	// Add the new implementation before the new method. Expect no diagnostics.
	t.Run("implementation before method", func(t *testing.T) {
		Run(t, pkg, func(t *testing.T, env *Env) {
			env.WriteWorkspaceFile("a/a.go", implementation)
			env.AfterChange(
				NoDiagnostics(ForFile("a/a.go")),
			)
			env.WriteWorkspaceFile("b/b.go", newMethod)
			env.AfterChange(
				NoDiagnostics(ForFile("a/a.go")),
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
			env.AfterChange(
				NoDiagnostics(ForFile("a/a.go")),
				NoDiagnostics(ForFile("b/b.go")),
			)
		})
	})
}

// Tests golang/go#38498. Delete a file and then force a reload.
// Assert that we no longer try to load the file.
func TestDeleteFiles(t *testing.T) {
	// TODO(rfindley): this test is brittle, because it depends on underspecified
	// logging behavior around loads.
	//
	// We should have a robust way to test loads. It should be possible to assert
	// on the specific loads that have occurred, and without the synchronization
	// problems associated with logging.

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
		WithOptions(
			// verboseOutput causes Snapshot.load to log package files.
			// (see the TODO above: this is brittle)
			Settings{"verboseOutput": true},
		).Run(t, pkg, func(t *testing.T, env *Env) {
			env.OpenFile("a/a.go")
			env.OpenFile("a/a_unneeded.go")
			env.Await(
				// Log messages are asynchronous to other events on the LSP stream, so we
				// can't use OnceMet or AfterChange here.
				LogMatching(protocol.Info, "a_unneeded.go", 1, false),
			)

			// Close and delete the open file, mimicking what an editor would do.
			env.CloseBuffer("a/a_unneeded.go")
			env.RemoveWorkspaceFile("a/a_unneeded.go")
			env.RegexpReplace("a/a.go", "var _ int", "fmt.Println(\"\")")
			env.AfterChange(
				Diagnostics(env.AtRegexp("a/a.go", "fmt")),
			)
			env.SaveBuffer("a/a.go")
			env.Await(
				// There should only be one log message containing
				// a_unneeded.go, from the initial workspace load, which we
				// check for earlier. If there are more, there's a bug.
				LogMatching(protocol.Info, "a_unneeded.go", 1, false),
				NoDiagnostics(ForFile("a/a.go")),
			)
		})
	})

	t.Run("delete then close", func(t *testing.T) {
		WithOptions(
			Settings{"verboseOutput": true},
		).Run(t, pkg, func(t *testing.T, env *Env) {
			env.OpenFile("a/a.go")
			env.OpenFile("a/a_unneeded.go")
			env.Await(
				LogMatching(protocol.Info, "a_unneeded.go", 1, false),
			)

			// Delete and then close the file.
			env.RemoveWorkspaceFile("a/a_unneeded.go")
			env.CloseBuffer("a/a_unneeded.go")
			env.RegexpReplace("a/a.go", "var _ int", "fmt.Println(\"\")")
			env.AfterChange(
				Diagnostics(env.AtRegexp("a/a.go", "fmt")),
			)
			env.SaveBuffer("a/a.go")
			env.Await(
				// There should only be one log message containing
				// a_unneeded.go, from the initial workspace load, which we
				// check for earlier. If there are more, there's a bug.
				LogMatching(protocol.Info, "a_unneeded.go", 1, false),
				NoDiagnostics(ForFile("a/a.go")),
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
		env.WriteWorkspaceFile("a/a3.go", "package a\n\nvar Hello int\n")
		env.RemoveWorkspaceFile("a/a1.go")
		env.WriteWorkspaceFile("a/a2.go", "package a; func _() {};")
		env.AfterChange(
			NoDiagnostics(ForFile("main.go")),
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
	WithOptions(
		WriteGoSum("."),
		ProxyFiles(proxy)).Run(t, mod, func(t *testing.T, env *Env) {
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
		env.AfterChange(
			env.DoneWithChangeWatchedFiles(),
			NoDiagnostics(ForFile("main.go")),
		)
	})
}

// Reproduces golang/go#40340.
func TestSwitchFromGOPATHToModuleMode(t *testing.T) {
	const files = `
-- foo/blah/blah.go --
package blah

const Name = ""
-- main.go --
package main

import "foo/blah"

func main() {
	_ = blah.Name
}
`
	WithOptions(
		InGOPATH(),
		Modes(Default), // golang/go#57521: this test is temporarily failing in 'experimental' mode
		EnvVars{"GO111MODULE": "auto"},
	).Run(t, files, func(t *testing.T, env *Env) {
		env.OpenFile("main.go")
		env.AfterChange(
			NoDiagnostics(ForFile("main.go")),
		)
		if _, err := env.Sandbox.RunGoCommand(env.Ctx, "", "mod", []string{"init", "mod.com"}, nil, true); err != nil {
			t.Fatal(err)
		}

		// TODO(golang/go#57558, golang/go#57512): file watching is asynchronous,
		// and we must wait for the view to be reconstructed before touching
		// main.go, so that the new view "knows" about main.go. This is a bug, but
		// awaiting the change here avoids it.
		env.AfterChange()

		env.RegexpReplace("main.go", `"foo/blah"`, `"mod.com/foo/blah"`)
		env.AfterChange(
			NoDiagnostics(ForFile("main.go")),
		)
	})
}

// Reproduces golang/go#40487.
func TestSwitchFromModulesToGOPATH(t *testing.T) {
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
		EnvVars{"GO111MODULE": "auto"},
	).Run(t, files, func(t *testing.T, env *Env) {
		env.OpenFile("foo/main.go")
		env.RemoveWorkspaceFile("foo/go.mod")
		env.AfterChange(
			Diagnostics(env.AtRegexp("foo/main.go", `"mod.com/blah"`)),
		)
		env.RegexpReplace("foo/main.go", `"mod.com/blah"`, `"foo/blah"`)
		env.AfterChange(
			NoDiagnostics(ForFile("foo/main.go")),
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
		env.AfterChange(
			NoDiagnostics(ForFile("a/a.go")),
			NoDiagnostics(ForFile("a/a_test.go")),
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
		env.AfterChange(
			NoDiagnostics(ForFile("a/a_test.go")),
			NoDiagnostics(ForFile("a/a2_test.go")),
		)
	})
}

// TestDebounceWatchedFiles checks that multiple didChangeWatchedFiles
// notifications are debounced into a single modification, and that
// all progress notifications are properly reported as completed.
func TestDebounceWatchedFiles(t *testing.T) {
	const files = `
-- go.mod --
module mod.com

go 1.14
-- a/a.go --
package a

func A() {}
`
	WithOptions(Settings{"watchedFileDelay": "100ms"}).Run(t, files, func(t *testing.T, env *Env) {
		env.OnceMet(
			InitialWorkspaceLoad,
			NoDiagnostics(ForFile("a/a.go")),
		)

		// Send DidChangeWatchedFiles notifications in rapid succession.
		// 5 is enough to ensure multiple notifications arrive
		// and exercise progress token coalescing without being too slow.
		for range 5 {
			err := env.Editor.Server.DidChangeWatchedFiles(env.Ctx, &protocol.DidChangeWatchedFilesParams{
				Changes: []protocol.FileEvent{
					{
						URI:  env.Sandbox.Workdir.URI("a/a.go"),
						Type: protocol.Changed,
					},
				},
			})
			if err != nil {
				t.Fatal(err)
			}
		}

		// The 5 notifications coalesce into a single diagnose pass, and all 5
		// progress notifications are completed together.
		title := server.DiagnosticWorkTitle(server.FromDidChangeWatchedFiles)
		env.Await(
			CompletedWork(title, 5, true),
			NoDiagnostics(ForFile("a/a.go")),
		)
	})
}

// TestDebounceFlushedByEditorAction checks that an explicit editor action
// immediately flushes pending watched
// file changes without waiting for the debounce timer to expire.
func TestDebounceFlushedByEditorAction(t *testing.T) {
	const files = `
-- go.mod --
module mod.com

go 1.14
-- a/a.go --
package a

func A() {}
-- b/b.go --
package b

func B() {}
`
	// Set a very large debounce delay (10s) so the timer will never fire on its own
	// without an editor action.
	WithOptions(Settings{"watchedFileDelay": "10s"}).Run(t, files, func(t *testing.T, env *Env) {
		env.OnceMet(
			InitialWorkspaceLoad,
			NoDiagnostics(ForFile("a/a.go")),
		)

		// 1. Modify a/a.go on disk with broken syntax.
		if err := env.Sandbox.Workdir.WriteFile(env.Ctx, "a/a.go", "package a; broken syntax"); err != nil {
			t.Fatal(err)
		}

		// Send watched file notification (enqueued in 10s debounce buffer).
		err := env.Editor.Server.DidChangeWatchedFiles(env.Ctx, &protocol.DidChangeWatchedFilesParams{
			Changes: []protocol.FileEvent{
				{
					URI:  env.Sandbox.Workdir.URI("a/a.go"),
					Type: protocol.Changed,
				},
			},
		})
		if err != nil {
			t.Fatal(err)
		}

		// 2. An explicit editor action (opening b/b.go) must immediately flush
		// the pending watched file changes, without waiting 10 seconds.
		env.OpenFile("b/b.go")

		// 3. Diagnostics for the broken a/a.go syntax should be produced promptly.
		env.Await(
			Diagnostics(ForFile("a/a.go")),
		)
	})
}

// TestDebounceOverlayOrdering checks that when a file is modified on disk
// and subsequently modified in an editor buffer while the on-disk change is still
// debounced, the on-disk change is processed first and the buffer overlay is applied
// second.
func TestDebounceOverlayOrdering(t *testing.T) {
	const files = `
-- go.mod --
module mod.com

go 1.14
-- a/a.go --
package a

const Msg = "initial"
`
	// Set a large debounce delay (10s) to keep disk events in the debounce queue.
	WithOptions(Settings{"watchedFileDelay": "10s"}).Run(t, files, func(t *testing.T, env *Env) {
		env.OnceMet(
			InitialWorkspaceLoad,
			NoDiagnostics(ForFile("a/a.go")),
		)

		// 1. Update a/a.go on disk.
		if err := env.Sandbox.Workdir.WriteFile(env.Ctx, "a/a.go", "package a\n\nconst Msg = \"disk\"\n"); err != nil {
			t.Fatal(err)
		}
		err := env.Editor.Server.DidChangeWatchedFiles(env.Ctx, &protocol.DidChangeWatchedFilesParams{
			Changes: []protocol.FileEvent{
				{
					URI:  env.Sandbox.Workdir.URI("a/a.go"),
					Type: protocol.Changed,
				},
			},
		})
		if err != nil {
			t.Fatal(err)
		}

		// 2. Open the file in the editor buffer with an overlay that has a syntax error.
		// Opening flushes the disk change first, and applies the overlay on top.
		env.OpenFile("a/a.go")
		env.EditBuffer("a/a.go", fake.NewEdit(0, 0, 3, 0, "package a\n\nconst Msg = \"overlay\"; syntax error\n"))

		// 3. Diagnostics should reflect the overlay buffer (syntax error).
		env.Await(
			Diagnostics(ForFile("a/a.go")),
		)

		// 4. Fix the syntax error in the overlay buffer and verify clean diagnostics.
		env.EditBuffer("a/a.go", fake.NewEdit(0, 0, 3, 0, "package a\n\nconst Msg = \"overlay\"\n"))
		env.Await(
			NoDiagnostics(ForFile("a/a.go")),
		)

		// 5. Close the buffer. Now the overlay is removed, and gopls falls back to
		// the flushed on-disk content ("disk").
		env.CloseBuffer("a/a.go")
		env.Await(
			NoDiagnostics(ForFile("a/a.go")),
		)
	})
}

// TestDebounceMaxDelayCap checks that a continuous stream of
// didChangeWatchedFiles notifications at intervals shorter than
// watchedFileDelay still flushes once the maximum debounce deadline is reached,
// rather than resetting the timer indefinitely.
func TestDebounceMaxDelayCap(t *testing.T) {
	const files = `
-- go.mod --
module mod.com

go 1.14
-- a/a.go --
package a

func A() {}
`
	WithOptions(Settings{"watchedFileDelay": "50ms"}).Run(t, files, func(t *testing.T, env *Env) {
		env.OnceMet(
			InitialWorkspaceLoad,
			NoDiagnostics(ForFile("a/a.go")),
		)

		stop := make(chan struct{})
		done := make(chan struct{})
		go func() {
			defer close(done)
			ticker := time.NewTicker(10 * time.Millisecond)
			defer ticker.Stop()
			for {
				select {
				case <-stop:
					return
				case <-ticker.C:
					_ = env.Editor.Server.DidChangeWatchedFiles(env.Ctx, &protocol.DidChangeWatchedFilesParams{
						Changes: []protocol.FileEvent{
							{
								URI:  env.Sandbox.Workdir.URI("a/a.go"),
								Type: protocol.Changed,
							},
						},
					})
				}
			}
		}()

		// Even though notifications arrive every 10ms (< 50ms watchedFileDelay),
		// the 500ms deadline cap forces a flush and completes work-done progress.
		title := server.DiagnosticWorkTitle(server.FromDidChangeWatchedFiles)
		env.Await(CompletedWork(title, 1, true))
		close(stop)
		<-done

		// Drain any notification sent between Await and close(stop) before shutdown.
		env.OpenFile("a/a.go")
		env.AfterChange()
	})
}
