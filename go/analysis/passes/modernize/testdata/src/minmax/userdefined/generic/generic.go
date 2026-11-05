package generic

func min[T ~int](a, b T) T {
	if a < b {
		return a
	}
	return b
}

func max[T ~int](a, b T) T {
	if a > b {
		return a
	}
	return b
}

// Built-in min/max cannot be explicitly instantiated, either as a function
// value or as the callee of a call with explicit type arguments.
var minimum = min[int]
var maximum = max[int](3, 2)
