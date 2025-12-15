// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// The modernize command suggests (or, with -fix, applies) fixes that
// clarify Go code by using more modern features.
//
// See [golang.org/x/tools/go/analysis/passes/modernize] for details.
package main

import (
	"golang.org/x/tools/go/analysis/multichecker"
	"golang.org/x/tools/go/analysis/passes/modernize"
)

func main() { multichecker.Main(modernize.Suite...) }
