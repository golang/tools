// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build go1.26

package a

import "sync"

func _(ptr *sync.Mutex) {
	_ = new(sync.Mutex)
	_ = new(*ptr) // want `call of new copies lock value: sync.Mutex`
)
