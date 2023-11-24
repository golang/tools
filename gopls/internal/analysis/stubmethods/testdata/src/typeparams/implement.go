// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package stubmethods

var _ I = Y{} // want "Implement I"

type I interface{ F() }

type X struct{}

func (X) F(string) {}

type Y struct{ X }
