// This program produces non-constant untyped integer values while
// evaluating shift expressions. These values are permitted transiently
// by the Go specification and must be accepted by the SSA sanity check.

package issue77067

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
