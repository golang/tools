package funcslice

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// The slice element is the only reference to min.
var functions = []func(int, int) int{min}
