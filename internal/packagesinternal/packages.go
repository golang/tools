// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package packagesinternal exposes internal-only fields from go/packages.
package packagesinternal

import (
	"golang.org/x/tools/internal/gocommand"
)

var GetForTest = func(p any) string { return "" }
var GetDepsErrors = func(p any) []*PackageError { return nil }

type PackageError struct {
	ImportStack []string // shortest path from package named on command line to this one
	Pos         string   // position of error (if present, file:line:col)
	Err         string   // the error itself
}

var GetGoCmdRunner = func(config any) *gocommand.Runner { return nil }

var SetGoCmdRunner = func(config any, runner *gocommand.Runner) {}

var TypecheckCgo int
var DepsErrors int // must be set as a LoadMode to call GetDepsErrors
var ForTest int    // must be set as a LoadMode to call GetForTest

var SetModFlag = func(config any, value string) {}
var SetModFile = func(config any, value string) {}
