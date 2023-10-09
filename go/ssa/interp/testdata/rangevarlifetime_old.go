//go:build go1.19

// goversion can be pinned to anything strictly before 1.22.

package main

// pre-go1.22 all of i will have the same address.
var same = func(a [3]int) []*int {
	var r []*int
	for i := range a {
		r = append(r, &i)
	}
	return r
}([3]int{})

func main() {
	if len(same) != 3 {
		panic(same)
	}
	for i := range same {
		for j := range same {
			if !(same[i] == same[j]) {
				panic(same)
			}
		}
	}
	for i := range same {
		if *(same[i]) != 2 {
			panic(same)
		}
	}
}
