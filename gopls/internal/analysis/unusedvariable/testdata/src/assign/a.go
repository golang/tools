// Copyright 2022 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package a

import (
	"fmt"
	"os"
)

type A struct {
	b int
}

func singleAssignment() {
	v := "s" // want `declared (and|but) not used`

	s := []int{ // want `declared (and|but) not used`
		1,
		2,
	}

	a := func(s string) bool { // want `declared (and|but) not used`
		return false
	}

	if 1 == 1 {
		s := "v" // want `declared (and|but) not used`
	}

	panic("I should survive")
}

func noOtherStmtsInBlock() {
	v := "s" // want `declared (and|but) not used`
}

func partOfMultiAssignment() {
	f, err := os.Open("file") // want `declared (and|but) not used`
	panic(err)
}

func sideEffects(cBool chan bool, cInt chan int) {
	b := <-c            // want `declared (and|but) not used`
	s := fmt.Sprint("") // want `declared (and|but) not used`
	a := A{             // want `declared (and|but) not used`
		b: func() int {
			return 1
		}(),
	}
	c := A{<-cInt}          // want `declared (and|but) not used`
	d := fInt() + <-cInt    // want `declared (and|but) not used`
	e := fBool() && <-cBool // want `declared (and|but) not used`
	f := map[int]int{       // want `declared (and|but) not used`
		fInt(): <-cInt,
	}
	g := []int{<-cInt}       // want `declared (and|but) not used`
	h := func(s string) {}   // want `declared (and|but) not used`
	i := func(s string) {}() // want `declared (and|but) not used`
}

func commentAbove() {
	// v is a variable
	v := "s" // want `declared (and|but) not used`
}

func commentBelow() {
	v := "s" // want `declared (and|but) not used`
	// v is a variable
}

func commentSpaceBelow() {
	v := "s" // want `declared (and|but) not used`

	// v is a variable
}

func fBool() bool {
	return true
}

func fInt() int {
	return 1
}
