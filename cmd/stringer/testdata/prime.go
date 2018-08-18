// Copyright 2014 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Enough gaps to trigger a map implementation of the method.
// Also includes a duplicate to test that it doesn't cause problems

package main

import "fmt"

type Prime int

const (
	p2  Prime = 2
	p3  Prime = 3
	p5  Prime = 5
	p7  Prime = 7
	p77 Prime = 7 // Duplicate; note that p77 doesn't appear below.
	p11 Prime = 11
	p13 Prime = 13
	p17 Prime = 17
	p19 Prime = 19
	p23 Prime = 23
	p29 Prime = 29
	p37 Prime = 31
	p41 Prime = 41
	p43 Prime = 43
)

func main() {
	ck(0, "Prime(0)")
	ck(1, "Prime(1)")
	ck(p2, "p2")
	ck(p3, "p3")
	ck(4, "Prime(4)")
	ck(p5, "p5")
	ck(p7, "p7")
	ck(p77, "p7")
	ck(p11, "p11")
	ck(p13, "p13")
	ck(p17, "p17")
	ck(p19, "p19")
	ck(p23, "p23")
	ck(p29, "p29")
	ck(p37, "p37")
	ck(p41, "p41")
	ck(p43, "p43")
	ck(44, "Prime(44)")

	fstr("p0", 0, false)
	fstr("p1", 0, false)
	fstr("p2", p2, true)
	fstr("p3", p3, true)
	fstr("p4", 0, false)
	fstr("p5", p5, true)
	fstr("p7", p7, true)
	fstr("p77", 0, false)
	fstr("p11", p11, true)
	fstr("p13", p13, true)
	fstr("p17", p17, true)
	fstr("p19", p19, true)
	fstr("p23", p23, true)
	fstr("p29", p29, true)
	fstr("p37", p37, true)
	fstr("p41", p41, true)
	fstr("p43", p43, true)
	fstr("p44", 0, false)
}

func ck(prime Prime, str string) {
	if fmt.Sprint(prime) != str {
		panic("prime.go: " + str)
	}
}

func fstr(str string, i Prime, ok bool) {
	res, found := PrimeFromString(str)
	if res != i || ok != found {
		panic("prime.go: " + str)
	}
}
