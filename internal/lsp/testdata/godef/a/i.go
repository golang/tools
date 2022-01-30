package a

import "time"

var globalVar = 0xfffff3

func _() {
	var hex, bin, test = 0xe34e, 0b1001001, time.Hour

	var (
		// Number with underscore
		numberWithUnderscore       = 10_000_000_000
		octal                int64 = 0o666
		bitwiseShift               = 10 << 20
		bitwiseXor                 = 0b111 ^ 0b101
		// No original value
		str = "string"
	)

	_ = globalVar            //@mark(globalVar, "globalVar"),hoverdef("globalVar", globalVar)
	_ = hex                  //@mark(hexVar, "hex"),hoverdef("hex", hexVar)
	_ = bin                  //@mark(binVar, "bin"),hoverdef("bin", binVar)
	_ = numberWithUnderscore //@mark(numberWithUnderscoreVar, "numberWithUnderscore"),hoverdef("numberWithUnderscore", numberWithUnderscoreVar)
	_ = octal                //@mark(octalVar, "octal"),hoverdef("octal", octalVar)
	_ = bitwiseShift         //@mark(bitwiseShiftVar, "bitwiseShift"),hoverdef("bitwiseShift", bitwiseShiftVar)
	_ = bitwiseXor           //@mark(bitwiseXorVar, "bitwiseXor"),hoverdef("bitwiseXor", bitwiseXorVar)
	_ = str                  //@mark(strVar, "str"),hoverdef("str", strVar)
}

const globalConst = 0xfffff3

func _() {
	const hex, bin = 0xe34e, 0b1001001

	const (
		numberWithUnderscore int64 = 10_000_000_000
		// Comment for test
		octal   = 0o777
		bitwise = 8 >> 1
		str     = "string"
	)

	_ = hex                  //@mark(hexConst, "hex"),hoverdef("hex", hexConst)
	_ = bin                  //@mark(binConst, "bin"),hoverdef("bin", binConst)
	_ = numberWithUnderscore //@mark(numberWithUnderscoreConst, "numberWithUnderscore"),hoverdef("numberWithUnderscore", numberWithUnderscoreConst)
	_ = globalConst          //@mark(globalConst, "globalConst"),hoverdef("globalConst", globalConst)
	_ = octal                //@mark(octalConst, "octal"),hoverdef("octal", octalConst)
	_ = bitwise              //@mark(bitwiseConst, "bitwise"),hoverdef("bitwise", bitwiseConst)
	_ = str                  //@mark(strConst, "str"),hoverdef("str", strConst)
}
