// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// This file contains a test for the shadowed variable checker with cross-file reference.

package crossfile

func ShadowGlobal() {
	{
		global := 1 // want "declaration of .global. shadows declaration at line 7 in other.go"
		_ = global
	}
	_ = global
}
