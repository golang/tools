package nanhandling

// User-defined min with float parameters - should NOT be removed due to NaN handling
func min(a, b float64) float64 {
	if a < b {
		return a
	} else {
		return b
	}
}

// User-defined max with float parameters - should NOT be removed due to NaN handling
func max(a, b float64) float64 {
	if a > b {
		return a
	} else {
		return b
	}
}
