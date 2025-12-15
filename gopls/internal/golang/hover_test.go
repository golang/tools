// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package golang

import "testing"

func TestSizeClass(t *testing.T) {
	// See GOROOT/src/runtime/msize.go for details.
	for _, test := range [...]struct{ size, class int64 }{
		{8, 8},
		{9, 16},
		{16, 16},
		{17, 24},
	} {
		got := sizeClass(test.size)
		if got != test.class {
			t.Errorf("sizeClass(%d) = %d, want %d", test.size, got, test.class)
		}
	}
}
