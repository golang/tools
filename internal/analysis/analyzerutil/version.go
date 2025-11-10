// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package analyzerutil

import (
	"go/ast"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/internal/packagepath"
	"golang.org/x/tools/internal/stdlib"
	"golang.org/x/tools/internal/versions"
)

// FileUsesGoVersion reports whether the specified file may use features of the
// specified version of Go (e.g. "go1.24").
//
// Tip: we recommend using this check "late", just before calling
// pass.Report, rather than "early" (when entering each ast.File, or
// each candidate node of interest, during the traversal), because the
// operation is not free, yet is not a highly selective filter: the
// fraction of files that pass most version checks is high and
// increases over time.
func FileUsesGoVersion(pass *analysis.Pass, file *ast.File, version string) bool {
	// Standard packages that are part of toolchain bootstrapping
	// are not considered to use a version of Go later than the
	// current bootstrap toolchain version.
	pkgpath := pass.Pkg.Path()
	if packagepath.IsStdPackage(pkgpath) &&
		stdlib.IsBootstrapPackage(pkgpath) &&
		versions.Before(version, stdlib.BootstrapVersion.String()) {
		return false // package must bootstrap
	}
	if versions.Before(pass.TypesInfo.FileVersions[file], version) {
		return false // file version is too old
	}
	return true // ok
}
