// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build wasm32
// +build wasm32

TEXT ·argint(SB),$0-16 // want `\[wasm32\] argint: wrong argument size 16; expected \$\.\.\.-8`
	MOVW x+0(FP), R0
	MOVW p+4(FP), R1
	MOVD x+0(FP), R0 // want `invalid MOVD of x\+0\(FP\); int is 4-byte value`
	I64Load p+4(FP) // want `invalid I64Load of p\+4\(FP\); \*byte is 4-byte value`
	I64Load32U p+4(FP)
	RET

// Results start at a pointer-aligned offset, even though int64 aligns to 8.
TEXT ·returnbyte(SB),$0-5
	MOVB R0, ret+4(FP)
	I64Store8 ret+4(FP)
	I64Store ret+4(FP) // want `invalid I64Store of ret\+4\(FP\); byte is 1-byte value`
	MOVB R0, ret+8(FP) // want `invalid offset ret\+8\(FP\); expected ret\+4\(FP\)`
	RET

// The return-PC slot is 8 bytes, unlike the 4-byte Go int and pointer.
TEXT ·stack(SB),$0-4
	MOVW 4(SP), R0
	MOVW 8(SP), R0 // want `8\(SP\) should be x\+0\(FP\)`
	MOVW 12(SP), R0 // want `use of 12\(SP\) points beyond argument frame`
	RET

TEXT ·aligned(SB),$0-24
	MOVB x_b+0(FP), R0
	MOVD x_n+8(FP), R0
	MOVW x_p+16(FP), R0
	RET

TEXT ·widths(SB),$0-32
	MOVB b+0(FP), R0
	MOVH h+2(FP), R0
	MOVW w+4(FP), R0
	MOVD d+8(FP), R0
	I32Load8U b+0(FP)
	I64Load8S b+0(FP)
	I32Load16S h+2(FP)
	I64Load16U h+2(FP)
	I32Load w+4(FP)
	I64Load32S w+4(FP)
	I64Load d+8(FP)
	F32Load f+16(FP)
	F64Load g+24(FP)
	I32Store8 b+0(FP)
	I64Store16 h+2(FP)
	I64Store32 w+4(FP)
	I64Store d+8(FP)
	F32Store f+16(FP)
	F64Store g+24(FP)
	I32Load b+0(FP) // want `invalid I32Load of b\+0\(FP\); uint8 is 1-byte value`
	I64Load32U h+2(FP) // want `invalid I64Load32U of h\+2\(FP\); uint16 is 2-byte value`
	F64Load f+16(FP) // want `invalid F64Load of f\+16\(FP\); float32 is 4-byte value`
	I32Store8 w+4(FP) // want `invalid I32Store8 of w\+4\(FP\); uint32 is 4-byte value`
	RET

TEXT ·imported(SB),$0-4
	CallImport // interpreted as writing result
	RET
