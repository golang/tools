package slicessort

import "sort"

func sortArgs() ([]int, func(int, int) bool) {
	s := []int{2, 1}
	return s, func(i, j int) bool { return s[i] < s[j] }
}

func multiValueSort() {
	// A multi-valued call may supply the complete argument list.
	sort.Slice(sortArgs())
}

type myint int

func _(s []myint) {
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] }) // want "sort.Slice can be modernized using slices.Sort"
}

func _(x *struct{ s []int }) {
	sort.Slice(x.s, func(first, second int) bool { return x.s[first] < x.s[second] }) // want "sort.Slice can be modernized using slices.Sort"
}

var sideEffectSlice []int
var sliceCalls int

func getSlice() []int {
	sliceCalls++
	return sideEffectSlice
}

func _() {
	// Replacing this call with slices.Sort(getSlice()) would reduce the
	// number of evaluations of getSlice from many to one.
	sort.Slice(getSlice(), func(i, j int) bool { return getSlice()[i] < getSlice()[j] }) // nope: slice expression may have effects
}

func _(s []int) {
	sort.Slice(s, func(i, j int) bool { return s[i] > s[j] }) // nope: wrong comparison operator
}

func _(s []int) {
	sort.Slice(s, func(i, j int) bool { return s[j] < s[i] }) // nope: wrong index var
}

func _(sense bool, s2 []struct{ x int }) {
	sort.Slice(s2, func(i, j int) bool { return s2[i].x < s2[j].x }) // nope: not a simple index operation

	// Regression test for a crash: the sole statement of a
	// comparison func body is not necessarily a return!
	sort.Slice(s2, func(i, j int) bool {
		if sense {
			return s2[i].x < s2[j].x
		} else {
			return s2[i].x > s2[j].x
		}
	})
}
