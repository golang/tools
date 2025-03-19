package stringscutprefix

import (
	. "strings"
)

// test supported cases of pattern 1
func _() {
	if HasPrefix(s, pre) { // want "HasPrefix \\+ TrimPrefix can be simplified to CutPrefix"
		a := TrimPrefix(s, pre)
		_ = a
	}
}

// test supported cases of pattern2
func _() {
	if after := TrimPrefix(s, pre); after != s { // want "TrimPrefix can be simplified to CutPrefix"
		println(after)
	}
	if after := TrimPrefix(s, pre); s != after { // want "TrimPrefix can be simplified to CutPrefix"
		println(after)
	}
}
