// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package goversions defines gopls's policy for which versions of Go it supports.
package goversion

import (
	"fmt"
	"strings"
)

// Support holds information about end-of-life Go version support.
//
// Exposed for testing.
type Support struct {
	// GoVersion is the Go version to which these settings relate.
	GoVersion int

	// DeprecatedVersion is the first version of gopls that no longer supports
	// this Go version.
	//
	// If unset, the version is already deprecated.
	DeprecatedVersion string

	// InstallGoplsVersion is the latest gopls version that supports this Go
	// version without warnings.
	InstallGoplsVersion string
}

// Supported maps Go versions to the gopls version in which support will
// be deprecated, and the final gopls version supporting them without warnings.
// Keep this in sync with gopls/README.md.
//
// Must be sorted in ascending order of Go version.
//
// Exposed (and mutable) for testing.
var Supported = []Support{
	{12, "", "v0.7.5"},
	{15, "", "v0.9.5"},
	{16, "", "v0.11.0"},
	{17, "", "v0.11.0"},
	{18, "v0.16.0", "v0.14.2"},
}

// OldestSupported is the last X in Go 1.X that this version of gopls
// supports without warnings.
//
// Exported for testing.
func OldestSupported() int {
	return Supported[len(Supported)-1].GoVersion + 1
}

// Message returns the message to display if the user has the given Go
// version, if any. The goVersion variable is the X in Go 1.X. If
// fromBuild is set, the Go version is the version used to build
// gopls. Otherwise, it is the go command version.
//
// The second component of the result indicates whether the message is
// an error, not a mere warning.
//
// If goVersion is invalid (< 0), it returns "", false.
func Message(goVersion int, fromBuild bool) (string, bool) {
	if goVersion < 0 {
		return "", false
	}

	for _, v := range Supported {
		if goVersion <= v.GoVersion {
			var msgBuilder strings.Builder

			isError := true
			if fromBuild {
				fmt.Fprintf(&msgBuilder, "Gopls was built with Go version 1.%d", goVersion)
			} else {
				fmt.Fprintf(&msgBuilder, "Found Go version 1.%d", goVersion)
			}
			if v.DeprecatedVersion != "" {
				// not deprecated yet, just a warning
				fmt.Fprintf(&msgBuilder, ", which will be unsupported by gopls %s. ", v.DeprecatedVersion)
				isError = false // warning
			} else {
				fmt.Fprint(&msgBuilder, ", which is not supported by this version of gopls. ")
			}
			fmt.Fprintf(&msgBuilder, "Please upgrade to Go 1.%d or later and reinstall gopls. ", OldestSupported())
			fmt.Fprintf(&msgBuilder, "If you can't upgrade and want this message to go away, please install gopls %s. ", v.InstallGoplsVersion)
			fmt.Fprint(&msgBuilder, "See https://go.dev/s/gopls-support-policy for more details.")

			return msgBuilder.String(), isError
		}
	}
	return "", false
}
