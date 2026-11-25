// This program used to cause the builder to emit TypeAssert
// instructions in the instance F[B] that assert the interface I to
// the concrete type B, which does not implement I. go/types rejects
// such impossible assertions in non-generic code, so the builder now
// omits them from instances, and the sanity pass checks for them.

package issue83062

type I interface{ M() }

type A struct{}

func (A) M() {}

type B struct{}

func F[T any](x I) {
	if v, ok := x.(T); ok {
		use(v)
	}
	var v, ok = x.(T)
	use(v)
	use(ok)
	switch x.(type) {
	case T:
		use(nil)
	}
	switch y := x.(type) {
	case T:
		use(y)
	}
	switch y := x.(type) {
	case A, T:
		use(y)
	}
	_ = x.(T)
}

func use(any) {}

func _() {
	F[A](A{})
	F[B](A{})
}
