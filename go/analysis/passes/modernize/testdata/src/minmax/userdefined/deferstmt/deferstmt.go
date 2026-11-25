package deferstmt

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// Built-in min/max cannot be deferred.
func calls() {
	defer min(3, 2)
	defer (max)(3, 2)
}
