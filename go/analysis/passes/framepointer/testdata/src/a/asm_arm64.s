// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

TEXT ·bad1(SB), 0, $0
	MOVD	$0, R29 // want `frame pointer is clobbered before saving`
	RET
TEXT ·bad2(SB), 0, $0
	MOVD	R1, R29 // want `frame pointer is clobbered before saving`
	RET
TEXT ·bad3(SB), 0, $0
	MOVD	6(R2), R29 // want `frame pointer is clobbered before saving`
	RET
TEXT ·bad4(SB), 0, $0
	LDP	0(R1), (R26, R29) // want `frame pointer is clobbered before saving`
	RET
TEXT ·bad5(SB), 0, $0
	AND	$0x1, R3, R29 // want `frame pointer is clobbered before saving`
	RET
TEXT ·good1(SB), 0, $0
	STPW 	(R29, R30), -32(RSP)
	MOVD	$0, R29 // this is ok
	LDPW	32(RSP), (R29, R30)
	RET
TEXT ·good2(SB), 0, $0
	MOVD	R29, R1
	MOVD	$0, R29 // this is ok
	MOVD	R1, R29
	RET
TEXT ·good3(SB), 0, $0
	CMP	R1, R2
	BEQ	skip
	MOVD	$0, R29 // this is ok
skip:
	RET
TEXT ·good4(SB), 0, $0
	RET
	MOVD	$0, R29 // this is ok
	RET
TEXT ·good5(SB), 0, $8
	MOVD	$0, R29 // this is ok
	RET
