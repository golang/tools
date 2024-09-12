// Copyright 2022 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package settings

import (
	"context"

	"mvdan.cc/gofumpt/format"
)

// GofumptFormat allows the gopls module to wire in a call to
// gofumpt/format.Source. langVersion and modulePath are used for some
// Gofumpt formatting rules -- see the Gofumpt documentation for details.
var GofumptFormat = func(ctx context.Context, langVersion, modulePath string, src []byte) ([]byte, error) {
	return format.Source(src, format.Options{
		LangVersion: langVersion,
		ModulePath:  modulePath,
	})
}
