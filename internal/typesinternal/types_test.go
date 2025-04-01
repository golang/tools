// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package typesinternal

import (
	"fmt"
	"go/ast"
	"go/types"
	"regexp"
	"testing"
)

func TestRequiresFullInfo(t *testing.T) {
	info := &types.Info{
		Uses:   map[*ast.Ident]types.Object{},
		Scopes: map[ast.Node]*types.Scope{},
	}
	panics(t, "Types, Instances, Defs, Implicits, Selections, FileVersions", func() {
		RequiresFullInfo(info)
	})

	// Shouldn't panic.
	RequiresFullInfo(NewTypesInfo())
}

// panics asserts that f() panics with with a value whose printed form matches the regexp want.
// Copied from go/analysis/internal/checker/fix_test.go.
func panics(t *testing.T, want string, f func()) {
	defer func() {
		if x := recover(); x == nil {
			t.Errorf("function returned normally, wanted panic")
		} else if m, err := regexp.MatchString(want, fmt.Sprint(x)); err != nil {
			t.Errorf("panics: invalid regexp %q", want)
		} else if !m {
			t.Errorf("function panicked with value %q, want match for %q", x, want)
		}
	}()
	f()
}
