package a

//go:fix inline
func f(x, y int) int { // want f:`goFixInline a.f`
	return y + x
}

func g() {
	f(1, 2) // want `Call of a.f should be inlined`

	f(h(1), h(2)) // want `Call of a.f should be inlined`
}

func h(int) int
