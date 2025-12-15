// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package web_test

import (
	"regexp"
	"runtime"
	"testing"

	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/gopls/internal/settings"
	. "golang.org/x/tools/gopls/internal/test/integration"
	"golang.org/x/tools/internal/testenv"
)

// TestAssembly is a basic test of the web-based assembly listing.
func TestAssembly(t *testing.T) {
	testenv.NeedsGoCommand1Point(t, 22) // for up-to-date assembly listing

	const files = `
-- go.mod --
module example.com

-- a/a.go --
package a

func f(x int) int {
	println("hello")
	defer println("world")
	return x
}

func g() {
	println("goodbye")
}

var v = [...]int{
	f(123),
	f(456),
}

func init() {
	f(789)
}
`
	Run(t, files, func(t *testing.T, env *Env) {
		env.OpenFile("a/a.go")

		// Get the report and do some minimal checks for sensible results.
		//
		// Use only portable instructions below! Remember that
		// this is a test of plumbing, not compilation, so
		// it's better to skip the tests, rather than refine
		// them, on any architecture that gives us trouble
		// (e.g. uses JAL for CALL, or BL<cc> for RET).
		// We conservatively test only on the two most popular
		// architectures.
		{
			loc := env.RegexpSearch("a/a.go", "println")
			report := asmFor(t, env, loc)
			checkMatch(t, true, report, `TEXT.*example.com/a.f`)
			switch runtime.GOARCH {
			case "amd64", "arm64":
				checkMatch(t, true, report, `CALL	runtime.printlock`)
				checkMatch(t, true, report, `CALL	runtime.printstring`)
				checkMatch(t, true, report, `CALL	runtime.printunlock`)
				checkMatch(t, true, report, `CALL	example.com/a.f.deferwrap`)
				checkMatch(t, true, report, `RET`)
				checkMatch(t, true, report, `CALL	runtime.morestack_noctxt`)
			}

			// Nested functions are also shown.
			//
			// The condition here was relaxed to unblock go.dev/cl/639515.
			checkMatch(t, true, report, `example.com/a.f.deferwrap`)

			// But other functions are not.
			checkMatch(t, false, report, `TEXT.*example.com/a.g`)
		}

		// Check that code in a package-level var initializer is found too.
		{
			loc := env.RegexpSearch("a/a.go", `f\(123\)`)
			report := asmFor(t, env, loc)
			switch runtime.GOARCH {
			case "amd64", "arm64":
				checkMatch(t, true, report, `TEXT.*example.com/a.init`)
				checkMatch(t, true, report, `MOV.?	\$123`)
				checkMatch(t, true, report, `MOV.?	\$456`)
				checkMatch(t, true, report, `CALL	example.com/a.f`)
			}
		}

		// And code in a source-level init function.
		{
			loc := env.RegexpSearch("a/a.go", `f\(789\)`)
			report := asmFor(t, env, loc)
			switch runtime.GOARCH {
			case "amd64", "arm64":
				checkMatch(t, true, report, `TEXT.*example.com/a.init`)
				checkMatch(t, true, report, `MOV.?	\$789`)
				checkMatch(t, true, report, `CALL	example.com/a.f`)
			}
		}
	})
}

// TestTestAssembly exercises assembly listing of tests.
func TestTestAssembly(t *testing.T) {
	testenv.NeedsGoCommand1Point(t, 22) // for up-to-date assembly listing

	const files = `
-- go.mod --
module example.com

-- a/a_test.go --
package a

import "testing"

func Test1(*testing.T) { println(0) }

-- a/a_x_test.go --
package a_test

import "testing"

func Test2(*testing.T) { println(0) }
`
	Run(t, files, func(t *testing.T, env *Env) {
		for _, test := range []struct {
			filename, symbol string
		}{
			{"a/a_test.go", "example.com/a.Test1"},
			{"a/a_x_test.go", "example.com/a_test.Test2"},
		} {
			env.OpenFile(test.filename)
			loc := env.RegexpSearch(test.filename, `println`)
			report := asmFor(t, env, loc)
			checkMatch(t, true, report, `TEXT.*`+regexp.QuoteMeta(test.symbol))
			switch runtime.GOARCH {
			case "amd64", "arm64":
				checkMatch(t, true, report, `CALL	runtime.printint`)
			}
		}
	})
}

// asmFor returns the HTML document served by gopls for a "Browse assembly"
// command at the specified location in an open file.
func asmFor(t *testing.T, env *Env, loc protocol.Location) []byte {
	_, content := codeActionWebPage(t, env, settings.GoAssembly, loc)
	return content
}
