package main

// This is a regression test for a bug (#69929) in
// the SSA interpreter in which it would not execute phis in parallel.
//
// The insert function below has interdependent phi nodes:
//
//	  entry:
//		t0 = *root       // t0 is x or y before loop
//		jump test
//	  body:
//		print(t5)      // t5 is x at loop entry
//		t3 = t5.Child    // t3 is x after loop
//		jump test
//	  test:
//		t5 = phi(t0, t3) // t5 is x at loop entry
//		t6 = phi(t0, t5) // t6 is y at loop entry
//		if t5 != nil goto body else done
//	  done:
//		print(t6)
//		return
//
// The two phis:
//
//	t5 = phi(t0, t3)
//	t6 = phi(t0, t5)
//
// must be executed in parallel as if they were written in Go
// as:
//
//	t5, t6 = phi(t0, t3), phi(t0, t5)
//
// with the second phi node observing the original, not
// updated, value of t5. (In more complex examples, the phi
// nodes may be mutually recursive, breaking partial solutions
// based on simple reordering of the phi instructions. See the
// Briggs paper for detail.)
//
// The correct behavior is print(1, root); print(2, root); print(3, root).
// The previous incorrect behavior had print(2, nil).

func main() {
	insert()
	print(3, root)
}

var root = new(node)

type node struct{ child *node }

func insert() {
	x := root
	y := x
	for x != nil {
		y = x
		print(1, y)
		x = x.child
	}
	print(2, y)
}

func print(order int, ptr *node) {
	println(order, ptr)
	if ptr != root {
		panic(ptr)
	}
}
