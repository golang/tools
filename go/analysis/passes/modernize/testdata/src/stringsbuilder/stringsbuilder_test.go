package stringsbuilder

func _() {
	var s string
	s += "before"
	for range 10 {
		s += "in" // nope: test file
		s += "in2"
	}
	s += "after"
	print(s)
}
