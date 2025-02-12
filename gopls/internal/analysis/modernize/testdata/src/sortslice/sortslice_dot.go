package sortslice

import . "slices"
import "sort"

func _(s []myint) {
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] }) // want "sort.Slice can be modernized using slices.Sort"
}

func _(x *struct{ s []int }) {
	sort.Slice(x.s, func(first, second int) bool { return x.s[first] < x.s[second] }) // want "sort.Slice can be modernized using slices.Sort"
}

func _(s []int) {
	sort.Slice(s, func(i, j int) bool { return s[i] > s[j] }) // nope: wrong comparison operator
}

func _(s []int) {
	sort.Slice(s, func(i, j int) bool { return s[j] < s[i] }) // nope: wrong index var
}

func _(s2 []struct{ x int }) {
	sort.Slice(s2, func(i, j int) bool { return s2[i].x < s2[j].x }) // nope: not a simple index operation
}

func _() { Clip([]int{}) }
