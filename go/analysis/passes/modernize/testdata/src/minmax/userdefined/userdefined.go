package userdefined

// User-defined min with float parameters - should NOT be removed due to NaN handling
func minFloat(a, b float64) float64 {
	if a < b {
		return a
	} else {
		return b
	}
}

// User-defined max with float parameters - should NOT be removed due to NaN handling
func maxFloat(a, b float64) float64 {
	if a > b {
		return a
	} else {
		return b
	}
}

// User-defined function with different name - should NOT be removed
func minimum(a, b int) int {
	if a < b {
		return a
	} else {
		return b
	}
}

// User-defined min with different logic - should NOT be removed
func minDifferent(a, b int) int {
	return a + b // Completely different logic
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

// Function with wrong signature - should NOT be removed
func minWrongSig(a int) int {
	return a
}

// Function with complex body that doesn't match pattern - should NOT be removed
func minComplex(a, b int) int {
	println("choosing min")
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


