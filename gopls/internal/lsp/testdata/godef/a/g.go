package a

import (
	"math"
	"time"
)

// dur is a constant of type time.Duration.
const dur = 15*time.Minute + 10*time.Second + 350*time.Millisecond //@dur,hoverdef("dur", dur)

// Numbers.
func _() {
	const hex, bin = 0xe34e, 0b1001001

	const (
		// no inline comment
		decimal = 153

		numberWithUnderscore int64 = 10_000_000_000
		octal                      = 0o777
		expr                       = 2 << (0b111&0b101 - 2)
		boolean                    = (55 - 3) == (26 * 2)
	)

	_ = decimal              //@mark(decimalConst, "decimal"),hoverdef("decimal", decimalConst)
	_ = hex                  //@mark(hexConst, "hex"),hoverdef("hex", hexConst)
	_ = bin                  //@mark(binConst, "bin"),hoverdef("bin", binConst)
	_ = numberWithUnderscore //@mark(numberWithUnderscoreConst, "numberWithUnderscore"),hoverdef("numberWithUnderscore", numberWithUnderscoreConst)
	_ = octal                //@mark(octalConst, "octal"),hoverdef("octal", octalConst)
	_ = expr                 //@mark(exprConst, "expr"),hoverdef("expr", exprConst)
	_ = boolean              //@mark(boolConst, "boolean"),hoverdef("boolean", boolConst)

	const ln10 = 2.30258509299404568401799145468436420760110148862877297603332790

	_ = ln10 //@mark(ln10Const, "ln10"),hoverdef("ln10", ln10Const)
}

// Iota.
func _() {
	const (
		a = 1 << iota
		b
	)

	_ = a //@mark(aIota, "a"),hoverdef("a", aIota)
	_ = b //@mark(bIota, "b"),hoverdef("b", bIota)
}

// Strings.
func _() {
	const (
		str     = "hello" + " " + "world"
		longStr = `Lorem ipsum dolor sit amet, consectetur adipiscing elit. Curabitur eget ipsum non nunc
molestie mattis id quis augue. Mauris dictum tincidunt ipsum, in auctor arcu congue eu.
Morbi hendrerit fringilla libero commodo varius. Vestibulum in enim rutrum, rutrum tellus
aliquet, luctus enim. Nunc sem ex, consectetur id porta nec, placerat vel urna.`
	)

	_ = str     //@mark(strConst, "str"),hoverdef("str", strConst)
	_ = longStr //@mark(longStrConst, "longStr"),hoverdef("longStr", longStrConst)
}

// Constants from other packages.
func _() {
	_ = math.MaxFloat32 //@mark(maxFloat32Const, "MaxFloat32"),hoverdef("MaxFloat32", maxFloat32Const)
}
