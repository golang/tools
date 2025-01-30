// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// This file contains tests of the printf checker's handling of non-constant
// format strings (golang/go#60529).

package nonconst

import (
	"fmt"
	"log"
	"os"
)

// As the language version is empty here, and the new check is gated on go1.24,
// diagnostics are suppressed here.
func nonConstantFormat(s string) {
	fmt.Printf(s)
	fmt.Printf(s, "arg")
	fmt.Fprintf(os.Stderr, s)
	log.Printf(s)
}
