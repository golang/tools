// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build !go1.24

package unfortunate

import "sync"

// TODO: Unfortunate cases

// Non-ideal error message:
// Since we're looking for Lock methods, sync.Once's underlying
// sync.Mutex gets called out, but without any reference to the sync.Once.
type LocalOnce sync.Once

func (LocalOnce) Bad() {} // want `Bad passes lock by value: unfortunate.LocalOnce contains sync.\b.*`

// False negative:
// LocalMutex doesn't have a Lock method.
// Nevertheless, it is probably a bad idea to pass it by value.
type LocalMutex sync.Mutex

func (LocalMutex) Bad() {} // WANTED: An error here :(
