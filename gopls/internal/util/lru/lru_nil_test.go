// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package lru_test

// TODO(rfindley): uncomment once -lang is at least go1.20.
// Prior to that language version, interfaces did not satisfy comparable.
// Note that we can't simply use //go:build go1.20, because we need at least Go
// 1.21 in the go.mod file for file language versions support!
/*
import (
	"testing"

	"golang.org/x/tools/gopls/internal/util/lru"
)

func TestSetUntypedNil(t *testing.T) {
	cache := lru.New[any, any](100 * 1e6)
	cache.Set(nil, nil, 1)
	if got, ok := cache.Get(nil); !ok || got != nil {
		t.Errorf("cache.Get(nil) = %v, %v, want nil, true", got, ok)
	}
}
*/
