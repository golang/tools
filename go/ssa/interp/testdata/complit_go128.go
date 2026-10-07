// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build go1.28

package main

// Test composite literals whose type is inferred from
// the assignment context, introduced in go1.28.
// See golang/go#12854 and golang/go#82039.

type T struct{ x, y int }

func val(t T) int       { return t.x + t.y }
func ptr(p *T) int      { return p.x + p.y }
func retVal() T         { return {1, 2} }
func retPtr() *T        { return {3, 4} }
func slice(s []int) int { return len(s) }
func retTwo() (T, *T)   { return {1, 2}, {3, 4} }

func retGeneric[P *T]() P      { return {5, 6} }
func argGeneric[P *T](p P) int { return (*T)(p).x + (*T)(p).y }

type S struct {
	V T
	P *T
}

// Pointers to arrays.
func ptrArray(p *[3]int) int { return p[0] + p[1] + p[2] }
func retPtrArray() *[3]int   { return {1, 2, 3} }

// Pointers to slices and maps (rarely useful, but legal).
func ptrSlice(p *[]int) int        { return len(*p) }
func ptrMap(p *map[string]int) int { return (*p)["a"] }
func retPtrSlice() *[]int          { return {1, 2, 3} }
func retPtrMap() *map[string]int   { return {"a": 7} }

func main() {
	if got := val({1, 2}); got != 3 {
		panic(got)
	}
	if got := ptr({3, 4}); got != 7 {
		panic(got)
	}
	if got := retVal(); got != (T{1, 2}) {
		panic(got)
	}
	if got := retPtr(); *got != (T{3, 4}) {
		panic(got)
	}
	if got := slice({1, 2, 3}); got != 3 {
		panic(got)
	}

	// Multi-value return.
	if v, p := retTwo(); v != (T{1, 2}) || *p != (T{3, 4}) {
		panic("retTwo")
	}

	// Type parameter whose core type is *T.
	if got := retGeneric[*T](); *got != (T{5, 6}) {
		panic(got)
	}
	if got := argGeneric[*T]({7, 8}); got != 15 {
		panic(got)
	}

	// Assignments and declarations.
	var p *T = {5, 6}
	if p.y != 6 {
		panic(p)
	}
	p = {7, 8}
	if p.y != 8 {
		panic(p)
	}

	// Distinct allocations.
	if retPtr() == retPtr() {
		panic("retPtr aliases")
	}

	// Conversion.
	q := (*T)({9, 10})
	if q.x != 9 {
		panic(q)
	}

	// Channel send.
	ch := make(chan *T, 1)
	ch <- {11, 12}
	if r := <-ch; r.y != 12 {
		panic(r)
	}

	// Map key and value.
	m := map[T]*T{}
	m[{1, 1}] = {2, 2}
	if r := m[T{1, 1}]; r == nil || r.x != 2 {
		panic(r)
	}

	// Struct fields.
	s := S{V: {1, 2}, P: {3, 4}}
	if s.V.y != 2 || s.P.y != 4 {
		panic(s)
	}
	s2 := S{{5, 6}, {7, 8}}
	if s2.V.y != 6 || s2.P.y != 8 {
		panic(s2)
	}

	// Reassigning a pointer variable from a literal that reads
	// through it allocates a new variable; the old one is unchanged.
	old := p
	p = {p.y, p.x}
	if p.x != 8 || p.y != 7 || old.x != 7 || old.y != 8 {
		panic("swap *T")
	}

	// Pointers to arrays.
	if got := ptrArray({1, 2, 3}); got != 6 {
		panic(got)
	}
	if got := ptrArray({2: 5}); got != 5 {
		panic(got)
	}
	if got := retPtrArray(); *got != [3]int{1, 2, 3} {
		panic(got)
	}
	if retPtrArray() == retPtrArray() {
		panic("retPtrArray aliases")
	}
	var pa *[3]int = {4, 5, 6}
	olda := pa
	pa = {pa[2], pa[1], pa[0]}
	if *pa != [3]int{6, 5, 4} || *olda != [3]int{4, 5, 6} {
		panic("swap *[3]int")
	}
	nestedArr := []*[2]int{{1, 2}, {3, 4}}
	if nestedArr[1][1] != 4 {
		panic(nestedArr[1][1])
	}
	cha := make(chan *[2]int, 1)
	cha <- {7, 8}
	if r := <-cha; r[1] != 8 {
		panic(r[1])
	}

	// Pointers to slices and maps.
	if got := ptrSlice({1, 2}); got != 2 {
		panic(got)
	}
	if got := ptrMap({"a": 5}); got != 5 {
		panic(got)
	}
	if got := len(*retPtrSlice()); got != 3 {
		panic(got)
	}
	if got := (*retPtrMap())["a"]; got != 7 {
		panic(got)
	}
	var ps *[]int = {4}
	if (*ps)[0] != 4 {
		panic((*ps)[0])
	}
	nested := []*[]int{{1}, {2, 3}}
	if got := len(*nested[1]); got != 2 {
		panic(got)
	}
	nm := map[*map[int]int]bool{{1: 2}: true}
	for k := range nm {
		if got := (*k)[1]; got != 2 {
			panic(got)
		}
	}
}
