// Copyright 2021 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package a

// This file tests facts produced by ctrlflow.

import (
	"hash/maphash"
	"log"
	"os"
	"runtime"
	"syscall"
	"testing"

	"lib"
)

var cond bool

func a() { // want a:"noReturn"
	if cond {
		b()
	} else {
		for {
		}
	}
}

func b() { // want b:"noReturn"
	select {}
}

func f(x int) { // no fact here
	switch x {
	case 0:
		os.Exit(0)
	case 1:
		panic(0)
	}
	// default case returns
}

type T int

func (T) method1() { // want method1:"noReturn"
	a()
}

func (T) method2() { // (may return)
	if cond {
		a()
	}
}

// Checking for the noreturn fact associated with F ensures that
// ctrlflow proved each of the listed functions was "noReturn".
func standardFunctions(x int) { // want standardFunctions:"noReturn"
	t := new(testing.T)
	switch x {
	case 0:
		t.FailNow()
	case 1:
		t.Fatal()
	case 2:
		t.Fatalf("")
	case 3:
		t.Skip()
	case 4:
		t.SkipNow()
	case 5:
		t.Skipf("")
	case 6:
		log.Fatal()
	case 7:
		log.Fatalf("")
	case 8:
		log.Fatalln()
	case 9:
		os.Exit(0)
	case 10:
		syscall.Exit(0)
	case 11:
		runtime.Goexit()
	case 12:
		log.Panic()
	case 13:
		log.Panicln()
	case 14:
		log.Panicf("")
	default:
		panic(0)
	}
}

func panicRecover() {
	defer func() { recover() }()
	panic(nil)
}

func noBody()

func g() {
	lib.CanReturn()
}

func h() { // want h:"noReturn"
	lib.NoReturn()
}

func returns() {
	print(1)
	print(2)
	print(3)
}

func nobody() // a function with no body is assumed to return

func hasPanic() { // want hasPanic:"noReturn"
	print(1)
	panic(2)
	print(3)
}

func hasSelect() { // want hasSelect:"noReturn"
	print(1)
	select {}
	print(3)
}

func infiniteLoop() { // want infiniteLoop:"noReturn"
	print(1)
	for {
	}
	print(3)
}

func ifElse(cond bool) { // want ifElse:"noReturn"
	print(1)
	if cond {
		hasSelect()
	} else {
		infiniteLoop()
	}
	print(3)
}

func swtch(x int) { // want swtch:"noReturn"
	print(1)
	switch x {
	case 1:
		hasSelect()
	case 2:
		goexit()
	case 3:
		logFatal()
	case 4:
		osexit()
	default:
		panic(3)
	}
}

func _if(cond bool) {
	print(1)
	if cond {
		hasSelect()
	}
	print(3)
}

func logFatal() { // want logFatal:"noReturn"
	print(1)
	log.Fatal("oops")
	print(2)
}

func testFatal(t *testing.T) { // want testFatal:"noReturn"
	print(1)
	t.Fatal("oops")
	print(2)
}

func goexit() { // want goexit:"noReturn"
	print(1)
	runtime.Goexit()
	print(2)
}

func osexit() { // want osexit:"noReturn"
	print(1)
	os.Exit(0)
	print(2)
}

func intrinsic() { // (no fact)

	// Comparable calls abi.EscapeNonString, whose body appears to panic;
	// but that's a lie, as EscapeNonString is a compiler intrinsic.
	// (go1.24 used a different intrinsic, maphash.escapeForHash.)
	maphash.Comparable[int](maphash.Seed{}, 0)
}
