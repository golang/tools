// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Pre-1.28 go/types leaves non-constant shift counts untyped.
// They must be evaluated as uint, including intermediate operations.

package main

func nestedShift(bytes []byte) uint32 {
	value := uint32(1)
	return value << (1 << bytes[3])
}

func arithmeticShiftCount(value uint) uint {
	return value >> ((1 >> value) + (1 >> value))
}

func runeShiftCount(value uint) uint {
	return value >> ('a' << value)
}

func highBitShiftCount(value uint) uint {
	return 1 << ((1 << value) >> (value - 1))
}

func highBitRuneShiftCount(value uint) uint {
	return 1 << (('a' << value) >> (value - 1))
}

func unaryShiftCount(value uint) uint {
	return 1 << (-(1 << value) + (1 << value) + 1)
}

func complementedShiftCount(value uint) uint {
	return 1 << (^(1 << value) & 3)
}

func genericShiftCount[T ~uint | ~uint32](value T, shift uint) T {
	return value << (1 << shift)
}

func signedShiftCount(value uint, shift int) uint {
	return value << (1 << shift)
}

func shiftAssign(value uint, shift int) uint {
	value <<= 1 << shift
	return value
}

func nonIntegerConstants(value uint, shift int) {
	if value<<(1<<1.0) != 16 || value<<(1<<(1+0i)) != 16 {
		panic("constant float or complex shift count")
	}
	if value<<(1.0<<shift) != 16 || value<<((1+0i)<<shift) != 16 {
		panic("non-constant float or complex shift count")
	}
}

func negativeShiftCount(shift int) {
	defer func() {
		if recover() == nil {
			panic("negative shift count did not panic")
		}
	}()
	// Go 1.28 rejects uint(1) << (-1 << shift).
	// An explicitly signed shift count must still panic when negative.
	_ = uint(1) << (int(-1) << shift)
}

func main() {
	if nestedShift([]byte{0, 0, 0, 2}) != 16 || arithmeticShiftCount(0) != 0 || runeShiftCount(0) != 0 {
		panic("nested shift count")
	}
	const wordBits = 32 << (^uint(0) >> 63)
	if highBitShiftCount(wordBits-1) != 4 || highBitRuneShiftCount(wordBits-1) != 4 {
		panic("shift count must use unsigned word-sized arithmetic")
	}
	if unaryShiftCount(2) != 2 || complementedShiftCount(0) != 4 {
		panic("unary shift count")
	}
	if genericShiftCount(uint(1), 2) != 16 || genericShiftCount(uint32(1), 2) != 16 {
		panic("generic shift count")
	}
	if signedShiftCount(1, 2) != 16 || shiftAssign(1, 2) != 16 {
		panic("signed or assignment shift count")
	}
	nonIntegerConstants(4, 1)
	negativeShiftCount(0)
}
