// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package b

import "./a"

func _() {
	var ex func(int) a.List[int]
	var fl func(a.List[int]) a.List[int]

	var l a.List[int]
	l = l.Map(ex).FlatMap(fl)

	var bl a.BiList[int, any]
	bl = bl.MapKeys(ex).Flip().FlatMapValues(fl).Flip()

	var id func(int) int

	var op a.Option[int]
	var _ int = op.MapIfPresent(id).Get()

	var ol a.OrderedList[int]
	var _ int = ol.Min().Get()

	var b a.Box[int]
	b.Set(42)
	var _ int = b.Get()
}
