package a

type PointerGood struct {
	P   *int
	buf [1000]uintptr
}

type PointerBad struct { // want "PointerBad has 4004 leading bytes of pointer data but optimal value is 4"
	buf [1000]uintptr
	P   *int
}

type PointerSorta struct {
	a struct {
		p *int
		q uintptr
	}
	b struct {
		p *int
		q [2]uintptr
	}
}

type PointerSortaBad struct { // want "PointerSortaBad has 16 leading bytes of pointer data but optimal value is 12"
	a struct {
		p *int
		q [2]uintptr
	}
	b struct {
		p *int
		q uintptr
	}
}

type MultiField struct { // want "MultiField has size 20 \\(allocator size class 24\\) but the optimal size is 12 \\(allocator size class 16\\) leading to a waste of 8 bytes \\(33%\\)"
	b      bool
	i1, i2 int
	a3     [3]bool
	_      [0]func()
}
