package stringscutprefix

import (
	"strings"
)

var (
	s, pre string
)

// test supported cases of pattern 1
func _() {
	if strings.HasPrefix(s, pre) { // want "HasPrefix \\+ TrimPrefix can be simplified to CutPrefix"
		a := strings.TrimPrefix(s, pre)
		_ = a
	}
	if strings.HasPrefix("", "") { // want "HasPrefix \\+ TrimPrefix can be simplified to CutPrefix"
		a := strings.TrimPrefix("", "")
		_ = a
	}
	if strings.HasPrefix(s, "") { // want "HasPrefix \\+ TrimPrefix can be simplified to CutPrefix"
		println([]byte(strings.TrimPrefix(s, "")))
	}
	if strings.HasPrefix(s, "") { // want "HasPrefix \\+ TrimPrefix can be simplified to CutPrefix"
		a, b := "", strings.TrimPrefix(s, "")
		_, _ = a, b
	}
	if strings.HasPrefix(s, "") { // want "HasPrefix \\+ TrimPrefix can be simplified to CutPrefix"
		a, b := strings.TrimPrefix(s, ""), strings.TrimPrefix(s, "") // only replace the first occurrence
		s = "123"
		b = strings.TrimPrefix(s, "") // only replace the first occurrence
		_, _ = a, b
	}

	var a, b string
	if strings.HasPrefix(s, "") { // want "HasPrefix \\+ TrimPrefix can be simplified to CutPrefix"
		a, b = "", strings.TrimPrefix(s, "")
		_, _ = a, b
	}
}

// test cases that are not supported by pattern1
func _() {
	ok := strings.HasPrefix("", "")
	if ok { // noop, currently it doesn't track the result usage of HasPrefix
		a := strings.TrimPrefix("", "")
		_ = a
	}
	if strings.HasPrefix(s, pre) {
		a := strings.TrimPrefix("", "") // noop, as the argument isn't the same
		_ = a
	}
	if strings.HasPrefix(s, pre) {
		var result string
		result = strings.TrimPrefix("", "") // noop, as we believe define is more popular.
		_ = result
	}
	if strings.HasPrefix("", "") {
		a := strings.TrimPrefix(s, pre) // noop, as the argument isn't the same
		_ = a
	}
	if s1 := s; strings.HasPrefix(s1, pre) {
		a := strings.TrimPrefix(s1, pre) // noop, as IfStmt.Init is present
		_ = a
	}
}

var value0 string

// test supported cases of pattern2
func _() {
	if after := strings.TrimPrefix(s, pre); after != s { // want "TrimPrefix can be simplified to CutPrefix"
		println(after)
	}
	if after := strings.TrimPrefix(s, pre); s != after { // want "TrimPrefix can be simplified to CutPrefix"
		println(after)
	}
	if after := strings.TrimPrefix(s, pre); s != after { // want "TrimPrefix can be simplified to CutPrefix"
		println(strings.TrimPrefix(s, pre)) // noop here
	}
	if after := strings.TrimPrefix(s, ""); s != after { // want "TrimPrefix can be simplified to CutPrefix"
		println(after)
	}
	var ok bool // define an ok variable to test the fix won't shadow it for its if stmt body
	_ = ok
	if after := strings.TrimPrefix(s, pre); after != s { // want "TrimPrefix can be simplified to CutPrefix"
		println(after)
	}
	var predefined string
	if predefined = strings.TrimPrefix(s, pre); s != predefined { // noop
		println(predefined)
	}
	if predefined = strings.TrimPrefix(s, pre); s != predefined { // noop
		println(&predefined)
	}
	var value string
	if value = strings.TrimPrefix(s, pre); s != value { // noop
		println(value)
	}
	lhsMap := make(map[string]string)
	if lhsMap[""] = strings.TrimPrefix(s, pre); s != lhsMap[""] { // noop
		println(lhsMap[""])
	}
	arr := make([]string, 0)
	if arr[0] = strings.TrimPrefix(s, pre); s != arr[0] { // noop
		println(arr[0])
	}
	type example struct {
		field string
	}
	var e example
	if e.field = strings.TrimPrefix(s, pre); s != e.field { // noop
		println(e.field)
	}
}

// test cases that not supported by pattern2
func _() {
	if after := strings.TrimPrefix(s, pre); s != pre { // noop
		println(after)
	}
	if after := strings.TrimPrefix(s, pre); after != pre { // noop
		println(after)
	}
	if strings.TrimPrefix(s, pre) != s {
		println(strings.TrimPrefix(s, pre))
	}
}
