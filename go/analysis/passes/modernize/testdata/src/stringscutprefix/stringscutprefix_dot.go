package stringscutprefix

import (
	. "strings"
)

// test supported cases of pattern 1 - CutPrefix
func _() {
	if HasPrefix(s, pre) { // want "HasPrefix \\+ TrimPrefix can be simplified to CutPrefix"
		a := TrimPrefix(s, pre)
		_ = a
	}
}

// test supported cases of pattern 1 - CutSuffix
func _() {
	if HasSuffix(s, suf) { // want "HasSuffix \\+ TrimSuffix can be simplified to CutSuffix"
		a := TrimSuffix(s, suf)
		_ = a
	}
}

// test supported cases of pattern2 - CutPrefix
func _() {
	if after := TrimPrefix(s, pre); after != s { // want "TrimPrefix can be simplified to CutPrefix"
		println(after)
	}
	if after := TrimPrefix(s, pre); s != after { // want "TrimPrefix can be simplified to CutPrefix"
		println(after)
	}
}

// test supported cases of pattern2 - CutSuffix
func _() {
	if before := TrimSuffix(s, suf); before != s { // want "TrimSuffix can be simplified to CutSuffix"
		println(before)
	}
	if before := TrimSuffix(s, suf); s != before { // want "TrimSuffix can be simplified to CutSuffix"
		println(before)
	}
}
