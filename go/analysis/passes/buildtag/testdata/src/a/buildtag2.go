// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build toolate
// +build toolate

package a

// want +1 `misplaced \+build comment`

// want +1 `misplaced //go:build comment`

var _ = `
// +build notacomment
`
