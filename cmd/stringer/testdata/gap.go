// Copyright 2014 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Gaps and an offset.

package main

import "fmt"

type Gap int

const (
	Two    Gap = 2
	Three  Gap = 3
	Five   Gap = 5
	Six    Gap = 6
	Seven  Gap = 7
	Eight  Gap = 8
	Nine   Gap = 9
	Eleven Gap = 11
)

func main() {
	ck(0, "Gap(0)")
	ck(1, "Gap(1)")
	ck(Two, "Two")
	ck(Three, "Three")
	ck(4, "Gap(4)")
	ck(Five, "Five")
	ck(Six, "Six")
	ck(Seven, "Seven")
	ck(Eight, "Eight")
	ck(Nine, "Nine")
	ck(10, "Gap(10)")
	ck(Eleven, "Eleven")
	ck(12, "Gap(12)")

	fstr("Zero", 0, false)
	fstr("One", 0, false)
	fstr("Two", Two, true)
	fstr("Three", Three, true)
	fstr("Four", 0, false)
	fstr("Five", Five, true)
	fstr("Six", Six, true)
	fstr("Seven", Seven, true)
	fstr("Eight", Eight, true)
	fstr("Nine", Nine, true)
	fstr("Ten", 0, false)
	fstr("Eleven", Eleven, true)
	fstr("Twelve", 0, false)
}

func ck(gap Gap, str string) {
	if fmt.Sprint(gap) != str {
		panic("gap.go: " + str)
	}
}

func fstr(str string, i Gap, ok bool) {
	res, found := GapFromString(str)
	if res != i || ok != found {
		panic("gap.go: " + str)
	}
}
