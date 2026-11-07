// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

TEXT ·bad1(SB), 0, $0
	MOVQ	$0, BP // want `frame pointer is clobbered before saving`
	RET
TEXT ·bad2(SB), 0, $0
	MOVQ	AX, BP // want `frame pointer is clobbered before saving`
	RET
TEXT ·bad3(SB), 0, $0
	MOVQ	6(AX), BP // want `frame pointer is clobbered before saving`
	RET
TEXT ·bad4(SB), 0, $0
	CMPQ	AX, BX
	JEQ	skip
	// Assume the above conditional branch is not taken
	MOVQ	$0, BP // want `frame pointer is clobbered before saving`
skip:
	RET
TEXT ·good1(SB), 0, $0
	PUSHQ	BP
	MOVQ	$0, BP // this is ok
	POPQ	BP
	RET
TEXT ·good2(SB), 0, $0
	MOVQ	BP, BX
	MOVQ	$0, BP // this is ok
	MOVQ	BX, BP
	RET
TEXT ·good3(SB), 0, $0
	CMPQ	AX, BX
	JMP	skip
	MOVQ	$0, BP // this is ok
skip:
	RET
TEXT ·good4(SB), 0, $0
	RET
	MOVQ	$0, BP // this is ok
	RET
TEXT ·good5(SB), 0, $8
	MOVQ	$0, BP // this is ok
	RET
