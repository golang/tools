package slicesdelete

var g struct{ f []int }

func h() []int { return []int{} }

var ch chan []int

func slicesdelete(test, other []byte, i int) {
	const k = 1
	_ = append(test[:i], test[i+1:]...) // want "Replace append with slices.Delete"

	_ = append(test[:i+1], test[i+2:]...) // want "Replace append with slices.Delete"

	_ = append(test[:i+1], test[i+1:]...) // not deleting any slice elements

	_ = append(test[:i], test[i-1:]...) // not deleting any slice elements

	_ = append(test[:i-1], test[i:]...) // want "Replace append with slices.Delete"

	_ = append(test[:i-2], test[i+1:]...) // want "Replace append with slices.Delete"

	_ = append(test[:i-2], other[i+1:]...) // different slices "test" and "other"

	_ = append(test[:i-2], other[i+1+k:]...) // cannot verify a < b

	_ = append(test[:i-2], test[11:]...) // cannot verify a < b

	_ = append(test[:1], test[3:]...) // want "Replace append with slices.Delete"

	_ = append(g.f[:i], g.f[i+k:]...) // want "Replace append with slices.Delete"

	_ = append(h()[:i], h()[i+1:]...) // potentially has side effects

	_ = append((<-ch)[:i], (<-ch)[i+1:]...) // has side effects

	_ = append(test[:3], test[i+1:]...) // cannot verify a < b

	_ = append(test[:i-4], test[i-1:]...) // want "Replace append with slices.Delete"

	_ = append(test[:1+2], test[3+4:]...) // want "Replace append with slices.Delete"

	_ = append(test[:1+2], test[i-1:]...) // cannot verify a < b
}

func issue73663(test, other []byte, i int32) {
	const k = 1
	_ = append(test[:i], test[i+1:]...) // want "Replace append with slices.Delete"

	_ = append(test[:i-1], test[i:]...) // want "Replace append with slices.Delete"

	_ = append(g.f[:i], g.f[i+k:]...) // want "Replace append with slices.Delete"

	type int string // int is shadowed, so no offered fix.
	_ = append(test[:i], test[i+1:]...)
}
