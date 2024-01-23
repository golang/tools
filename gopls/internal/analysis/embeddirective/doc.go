// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package embeddirective defines an Analyzer that validates //go:embed directives.
// The analyzer defers fixes to its parent golang.Analyzer.
//
// # Analyzer embed
//
// embed: check //go:embed directive usage
//
// This analyzer checks that the embed package is imported if //go:embed
// directives are present, providing a suggested fix to add the import if
// it is missing.
//
// This analyzer also checks that //go:embed directives precede the
// declaration of a single variable.
package embeddirective
