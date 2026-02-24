// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package hover

import (
	"os"
	"testing"

	. "golang.org/x/tools/gopls/internal/test/integration"
	"golang.org/x/tools/gopls/internal/util/bug"
)

func TestMain(m *testing.M) {
	bug.PanicOnBugs = true
	os.Exit(Main(m))
}

// TestIssue77675 verifies that hovering over a raw string literal containing
// Windows line endings (\r\n) does not cause an out-of-bounds panic.
func TestIssue77675(t *testing.T) {
	const src = `
-- go.mod --
module mod.com

go 1.20

-- main.go --
package main

func _() {
    _ = ` + "`" + `foo

bar
baz` + "`" + `
}
`
	WithOptions(
		WindowsLineEndings(),
	).Run(t, src, func(t *testing.T, env *Env) {
		env.OpenFile("main.go")
		env.Await(env.DoneWithOpen())
		loc := env.RegexpSearch("main.go", "baz()")
		content, l := env.Hover(loc)
		if !l.Empty() {
			t.Errorf("hover expect empty range got: %v", l)
		}
		if content != nil {
			t.Errorf("hover expect empty result got: %v", content)
		}
	})
}
