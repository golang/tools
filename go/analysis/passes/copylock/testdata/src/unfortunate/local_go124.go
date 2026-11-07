// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build go1.24

package unfortunate

import "sync"

// Cases where the interior sync.noCopy shows through.

type LocalOnce sync.Once

func (LocalOnce) Bad() {} // want "Bad passes lock by value: unfortunate.LocalOnce contains sync.noCopy"

type LocalMutex sync.Mutex

func (LocalMutex) Bad() {} // want "Bad passes lock by value: unfortunate.LocalMutex contains sync.noCopy"
