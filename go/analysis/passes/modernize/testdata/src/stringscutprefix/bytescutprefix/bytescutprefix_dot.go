package bytescutprefix

import (
	. "bytes"
)

var bss, bspre, bssuf []byte

// test supported cases of pattern 1
func _() {
	if HasPrefix(bss, bspre) { // want "HasPrefix \\+ TrimPrefix can be simplified to CutPrefix"
		a := TrimPrefix(bss, bspre)
		_ = a
	}

	if HasSuffix(bss, bssuf) { // want "HasSuffix \\+ TrimSuffix can be simplified to CutSuffix"
		b := TrimSuffix(bss, bssuf)
		_ = b
	}
}
