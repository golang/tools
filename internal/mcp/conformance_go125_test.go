// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build go1.25

package mcp

import (
	"testing"
	"testing/synctest"
)

func runSyncTest(t *testing.T, f func(t *testing.T)) {
	synctest.Test(t, f)
}
