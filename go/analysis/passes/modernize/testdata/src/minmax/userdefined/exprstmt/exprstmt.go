package exprstmt

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

// Built-in min/max cannot be used as expression statements.
func calls() {
	(min)(3, 2)
	(max(3, 2))
}
