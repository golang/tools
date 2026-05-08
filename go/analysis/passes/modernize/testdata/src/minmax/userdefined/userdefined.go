package userdefined

// User-defined function with different name - should NOT be removed
func minimum(a, b int) int {
	if a < b {
		return a
	} else {
		return b
	}
}

// Method on a type - should NOT be removed
type MyType struct{}

func (m MyType) min(a, b int) int {
	if a < b {
		return a
	} else {
		return b
	}
}

// min returns the smaller of two values.
func min(a, b int) int { // want "user-defined min function is equivalent to built-in min and can be removed"
	if a < b {
		return a
	} else {
		return b
	}
}

// max returns the larger of two values.
func max(a, b int) int { // want "user-defined max function is equivalent to built-in max and can be removed"
	if a > b {
		return a
	}
	return b
}
