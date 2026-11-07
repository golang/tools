package notuserparams

// User-defined max with correct logic but wrong vars - should NOT be removed
func max(a, b int) int {
	if a < 5 {
		return a
	} else {
		return 5
	}
}

// User-defined min with correct logic but wrong vars - should NOT be removed
func min(a, b int) int {
	if 5 < b {
		return 5
	} else {
		return b
	}
}
