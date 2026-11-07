package nonstrict

// min with <= operator - should be detected and removed
func min(a, b int) int { // want "user-defined min function is equivalent to built-in min and can be removed"
	if a <= b {
		return a
	} else {
		return b
	}
}

// max with >= operator - should be detected and removed
func max(a, b int) int { // want "user-defined max function is equivalent to built-in max and can be removed"
	if a >= b {
		return a
	}
	return b
}
