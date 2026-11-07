package differentlogic

// User-defined min with different logic - should NOT be removed
func min(a, b int) int {
	return a + b // Completely different logic
}

// Function with complex body that doesn't match pattern - should NOT be removed
func max(a, b int) int {
	println("choosing min")
	if a < b {
		return a
	} else {
		return b
	}
}
