// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package modindex

// backdoors for tests

var Uniquify = uniquify

// Create always creates a new index for the
// specified Go module cache directory.
// On success it returns the current index.
func Create(gomodcache string) (*Index, error) {
	return update(gomodcache, nil)
}
