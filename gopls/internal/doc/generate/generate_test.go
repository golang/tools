// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"testing"

	"golang.org/x/tools/internal/testenv"
)

func TestGenerated(t *testing.T) {
	testenv.NeedsGoPackages(t)
	testenv.NeedsLocalXTools(t)

	ok, err := doMain(false)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Error("documentation needs updating. Run: cd gopls && go generate ./...")
	}
}
