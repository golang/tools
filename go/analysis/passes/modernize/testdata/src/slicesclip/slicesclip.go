package slicesclip

var g struct{ f []int }

func h() []int { return []int{} }

var ch chan []int

func _(test, other []byte, i int) {
	_ = test[:len(test):len(test)] // want `x\[:len\(x\):len\(x\)\] can be simplified using slices\.Clip`

	_ = test[1:len(test):len(test)] // non-zero low index: no match

	_ = test[:len(test)] // ordinary two-index slice: no match

	_ = test[:len(other):len(other)] // different slice variable: no match

	_ = test[:len(test):len(other)] // mismatched high/max: no match

	_ = g.f[:len(g.f):len(g.f)] // want `x\[:len\(x\):len\(x\)\] can be simplified using slices\.Clip`

	_ = h()[:len(h()):len(h())] // potentially has side effects: no match

	_ = (<-ch)[:len(<-ch):len(<-ch)] // has side effects: no match

	if len(test) > 0 {
		test = test[:len(test):len(test)] // want `x\[:len\(x\):len\(x\)\] can be simplified using slices\.Clip`
	}

	_ = append(other, test[:len(test):len(test)]...) // want `x\[:len\(x\):len\(x\)\] can be simplified using slices\.Clip`

	_ = i
}

func shadowed(test []byte) {
	len := func(_ []byte) int { return 0 }
	_ = test[:len(test):len(test)] // len is shadowed: no match
}

func arrayCase() {
	var a [3]int
	_ = a[:len(a):len(a)] // array, not slice: no match

	pa := &a
	_ = pa[:len(pa):len(pa)] // pointer to array, not slice: no match
}
