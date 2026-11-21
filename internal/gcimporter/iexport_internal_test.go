// Copyright 2021 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package gcimporter

// This file defines white-box tests, and hooks for the external tests.

import "testing"

// ConstantToFloat exposes constantToFloat to the external tests.
var ConstantToFloat = constantToFloat

// TestFileInfosField checks that mayHaveLineInfos can find the
// token.File.infos field. If this fails, the layout has changed, and
// fileInfosField must be updated; until then the encoder conservatively
// (and expensively) computes adjusted positions for every file.
func TestFileInfosField(t *testing.T) {
	if fileInfosField < 0 {
		t.Fatal("unexpected layout of token.File; update fileInfosField")
	}
}
