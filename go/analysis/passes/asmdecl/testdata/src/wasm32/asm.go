// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package wasm32

func argint(x int, p *byte)
func returnbyte(x int) byte
func stack(x int)
func aligned(x struct {
	b byte
	n int64
	p *byte
})
func widths(b uint8, h uint16, w uint32, d uint64, f float32, g float64)
func imported() int
