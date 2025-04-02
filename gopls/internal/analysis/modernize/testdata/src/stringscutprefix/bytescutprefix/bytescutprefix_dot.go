package bytescutprefix

import (
	. "bytes"
)

var bss, bspre []byte

// test supported cases of pattern 1
func _() {
	if HasPrefix(bss, bspre) { // want "HasPrefix \\+ TrimPrefix can be simplified to CutPrefix"
		a := TrimPrefix(bss, bspre)
		_ = a
	}
}
