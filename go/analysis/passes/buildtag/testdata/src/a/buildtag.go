// Copyright 2013 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// This file contains tests for the buildtag checker.

// want +1 `possible malformed \+build comment`
// +builder
//go:build ok
// +build ok

// Mention +build // want `possible malformed \+build comment`

// want +1 `misplaced \+build comment`
package a

// want +1 `misplaced \+build comment`

var _ = 3

var _ = `
// +build notacomment
`
