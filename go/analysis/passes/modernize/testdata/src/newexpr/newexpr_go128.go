//go:build go1.28

package newexpr

// Composite literals whose type is inferred from the
// assignment context (go1.28; see golang/go#82039).

type point struct{ x, y int }

var (
	_ = varOf[point]({1, 2})  // nope: new({1, 2}) is ill-typed
	_ = varOf[*point]({1, 2}) // nope: likewise
)
