package importsunsafe

import "unsafe"

type S struct {
	F, G int
}

func _() {
	var s S
	s.F = 1
	// This write to G is used below, because &s.F allows access to all of s, but
	// the analyzer would naively report it as unused. For this reason, we
	// silence the analysis if unsafe is imported.
	s.G = 2

	ptr := unsafe.Pointer(&s.F)
	t := (*S)(ptr)
	println(t.G)
}
