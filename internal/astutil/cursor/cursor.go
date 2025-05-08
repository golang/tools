// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package cursor is deprecated; use [inspector.Cursor].
package cursor

import "golang.org/x/tools/go/ast/inspector"

//go:fix inline
type Cursor = inspector.Cursor

//go:fix inline
func Root(in *inspector.Inspector) inspector.Cursor { return in.Root() }

//go:fix inline
func At(in *inspector.Inspector, index int32) inspector.Cursor { return in.At(index) }
