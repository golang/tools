package a

func f() {
	One() // want `inline call of a.One`

	new(T).Two() // want `inline call of \(a.T\).Two`
}

type T struct{}

//go:fix inline
func One() int { return one } // want One:`goFixInline a.One`

const one = 1

//go:fix inline
func (T) Two() int { return 2 } // want Two:`goFixInline \(a.T\).Two`
