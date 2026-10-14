package d

type message interface{ PR() }

func noparam() {
	var messageT message
	messageT.PR() // want "nil dereference in dynamic method call"
}

func paramNonnil[T message]() {
	var messageT T
	messageT.PR() // cannot conclude messageT is nil.
}

func instance() {
	// buildssa.BuilderMode does not include InstantiateGenerics.
	paramNonnil[message]() // no warning is expected as param[message] id not built.
}

func param[T interface {
	message
	~*int | ~chan int
}]() {
	var messageT T // messageT is nil.
	messageT.PR()  // nil receiver may be okay. See param[nilMsg].
}

type nilMsg chan int

func (m nilMsg) PR() {
	if m == nil {
		print("not an error")
	}
}

var G func() = param[nilMsg] // no warning

func allNillable[T ~*int | ~chan int]() {
	var x, y T  // both are nillable and are nil.
	if x != y { // want "impossible condition: nil != nil"
		print("unreachable")
	}
}

func notAll[T ~*int | ~chan int | ~int]() {
	var x, y T  // neither are nillable due to ~int
	if x != y { // no warning
		print("unreachable")
	}
}

func noninvoke[T ~func()]() {
	var x T
	x() // want "nil dereference in dynamic function call"
}

// Indexing a type parameter whose type set mixes *array and slice
// types has no core type, but is a nil dereference nonetheless.
func indexMixed[P *[4]int | []int](p P) int {
	if p == nil {
		return p[0] // want "nil dereference in index operation"
	}
	return 0
}

func indexArrayPtrs[P *[4]int | *myArray](p P) int {
	if p == nil {
		return p[0] // want "nil dereference in array index operation"
	}
	return 0
}

type myArray [4]int // *[4]int and *myArray have different underlying types

// After a failed type assertion, the result is the zero value of the
// asserted type, which is nil for interfaces and nillable type parameters.
func assertIface(x any) {
	if r, ok := x.(interface{ Read() }); !ok {
		if r != nil { // want "impossible condition: nil != nil"
			print()
		}
	}
}

func assertNillableTParam[P *int | []int](x any) {
	if p, ok := x.(P); !ok {
		if p != nil { // want "impossible condition: nil != nil"
			print()
		}
	}
}

func assertUnrestrictedTParam[P any](x any) {
	if p, ok := x.(P); !ok {
		print(p) // zero value of P need not be nil
	}
}
