// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package version manages the gopls version.
//
// The VersionOverride variable may be used to set the gopls version at link
// time.
package version

import "runtime/debug"

var VersionOverride = ""

// Version returns the gopls version.
//
// By default, this is read from runtime/debug.ReadBuildInfo, but may be
// overridden by the [VersionOverride] variable.
func Version() string {
	if VersionOverride != "" {
		return VersionOverride
	}
	if info, ok := debug.ReadBuildInfo(); ok {
		if info.Main.Version != "" {
			return info.Main.Version
		}
	}
	return "(unknown)"
}
