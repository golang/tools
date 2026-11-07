package wrongoperators

// min function with max logic - should NOT be detected (wrong logic)
func min(a, b int) int {
	if a >= b { // This is max logic, not min logic
		return a
	} else {
		return b
	}
}

// max function with min logic - should NOT be detected (wrong logic)
func max(a, b int) int {
	if a <= b { // This is min logic, not max logic
		return a
	} else {
		return b
	}
}
