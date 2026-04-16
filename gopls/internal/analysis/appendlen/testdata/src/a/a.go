package a

type myInts []int

func basic(input []int) []int {
	out := make([]int, len(input)) // want "slice created with len\\(input\\) is appended to while iterating over the same value"
	for _, in := range input {
		out = append(out, in)
	}
	return out
}

func assign(input []string) []string {
	var out []string
	out = make([]string, len(input)) // want "slice created with len\\(input\\) is appended to while iterating over the same value"
	for _, in := range input {
		out = append(out, in)
	}
	return out
}

func declared(input [][]byte) [][]byte {
	var out = make([][]byte, len(input)) // want "slice created with len\\(input\\) is appended to while iterating over the same value"
	for _, in := range input {
		if len(in) > 0 {
			out = append(out, in)
		}
	}
	return out
}

func namedSlice(input []int) myInts {
	out := make(myInts, len(input)) // want "slice created with len\\(input\\) is appended to while iterating over the same value"
	for _, in := range input {
		out = append(out, in)
	}
	return out
}

func alreadyGood(input []int) []int {
	out := make([]int, 0, len(input))
	for _, in := range input {
		out = append(out, in)
	}
	return out
}

func differentRange(input, other []int) []int {
	out := make([]int, len(input))
	for _, in := range other {
		out = append(out, in)
	}
	return out
}

func classicFor(input []int) []int {
	out := make([]int, len(input)) // want "slice created with len\\(input\\) is appended to while iterating over the same value"
	for i := 0; i < len(input); i++ {
		out = append(out, input[i])
	}
	return out
}

func classicForDifferent(input, other []int) []int {
	out := make([]int, len(input))
	for i := 0; i < len(other); i++ {
		out = append(out, other[i])
	}
	return out
}

func classicForNonZeroStart(input []int) []int {
	out := make([]int, len(input))
	for i := 1; i < len(input); i++ {
		out = append(out, input[i])
	}
	return out
}

func classicForLE(input []int) []int {
	out := make([]int, len(input))
	for i := 0; i <= len(input)-1; i++ {
		out = append(out, input[i])
	}
	return out
}

func classicForPredeclaredIndex(input []int) []int {
	out := make([]int, len(input))
	var i int
	for i = 0; i < len(input); i++ {
		out = append(out, input[i])
	}
	return out
}

func notImmediate(input []int) []int {
	out := make([]int, len(input))
	_ = cap(out)
	for _, in := range input {
		out = append(out, in)
	}
	return out
}

func unusedAppend(input []int) {
	out := make([]int, len(input))
	for _, in := range input {
		_ = append(out, in)
	}
}
