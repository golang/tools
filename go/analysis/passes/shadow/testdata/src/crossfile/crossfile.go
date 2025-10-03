// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// This file contains a test for the shadowed variable checker with cross-file reference.

package crossfile

import "fmt"

func ShadowGlobal() {
	global := 1 // want "declaration of .global. shadows declaration at line 8 in other.go"
	_ = global
}

func ShadowGlobalWithDifferentType() {
	global := "text" // OK: different type.
	_ = global
}

func ShadowPackageName() {
	fmt := "text" // want "declaration of .fmt. shadows declaration at line 9"
	_ = fmt
}

// To import fmt package
func PrintHelper() {
	fmt.Println()
}
