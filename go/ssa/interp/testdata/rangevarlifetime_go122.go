//go:build go1.22

package main

// After go1.22, each i will have a distinct address.
var distinct = func(a [3]int) []*int {
	var r []*int
	for i := range a {
		r = append(r, &i)
	}
	return r
}([3]int{})

func main() {
	if len(distinct) != 3 {
		panic(distinct)
	}
	for i := 0; i < 3; i++ {
		if i != *(distinct[i]) {
			panic(distinct)
		}
	}
}
