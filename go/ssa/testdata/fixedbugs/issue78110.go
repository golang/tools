// This program used to cause the builder to emit Next instructions
// with types based on the (k, v) variables and not the iterated map
// (cases 1 and 2 of issue 78110) due to the implicit interface
// conversion in the assignment from iterator value to variable.
// The sanity pass now checks that their types are identical.

package issue78110

func rangeConcreteMap() {
	var v any
	for _, v = range map[int]int{} {
		_ = v
	}
}

func rangeInterfaceMap() {
	var v any
	for _, v = range map[int]interface{ M() }{} {
		_ = v
	}
}

type I interface{ M() }
type J interface {
	I
	N()
}

func boundMethodClosure(j J) {
	_ = j.M
}
