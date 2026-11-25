package funcvalues

// Built-in min/max cannot replace these first-class function values.
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

var minimum = min

func apply(f func(int, int) int) int { return f(3, 2) }

var maximum = apply(max)

// Direct calls do not make it safe to delete a function also used as a value.
func calls() (int, int) { return min(3, 2), max(3, 2) }
