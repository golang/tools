// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package filewatcher

// This file defines things (and opens backdoors) needed only by tests.

// SetAfterAddHook sets a hook to be called after a path is added to the watcher.
// This is used in tests to inspect the error returned by the underlying watcher.
func SetAfterAddHook(f func(string, error)) {
	afterAddHook = f
}
