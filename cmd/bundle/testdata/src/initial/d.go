package initial

type Atype[E any] struct {
	data E
}

func (a Atype[E]) foo() E {
	return a.data
}

// Embedded generic struct
type Btype[E any] struct {
	Atype[[2]E]
}

func (b Btype[E]) foo() (E, E) {
	x := b.Atype.foo()
	return x[0], x[1]
}

func NewBtype[E any](e0, e1 E) *Btype[E] {
	return &Btype[E]{
		Atype: Atype[[2]E]{
			data: [2]E{e0, e1},
		},
	}
}
