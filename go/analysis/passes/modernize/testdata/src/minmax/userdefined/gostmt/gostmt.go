package gostmt

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

// Built-in min/max cannot be called in go statements.
func calls() {
	go min(3, 2)
	go (max)(3, 2)
}
