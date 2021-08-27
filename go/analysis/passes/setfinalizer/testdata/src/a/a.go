// Copyright 2021 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// This file contains tests for the setfinalizer checker.

package testdata

import (
	"runtime"
)

type Tint int
type Tintptr *int
type Tintptr2 *Tint
type NonFunc struct{}
type IntFinalizer func(*int)
type TintFinalizer func(*Tint)
type TintptrFinalizer func(Tintptr)
type Tintptr2Finalizer func(Tintptr2)

func _() {
	// check first arg
	runtime.SetFinalizer(nil, nil)      // want "runtime\\.SetFinalizer: first argument is nil"
	runtime.SetFinalizer(0, nil)        // want "runtime\\.SetFinalizer: first argument is int, not pointer"
	runtime.SetFinalizer("string", nil) // want "runtime\\.SetFinalizer: first argument is string, not pointer"
	var i int
	runtime.SetFinalizer(i, nil)       // want "runtime\\.SetFinalizer: first argument is int, not pointer"
	runtime.SetFinalizer(Tint(i), nil) // want "runtime\\.SetFinalizer: first argument is a\\.Tint, not pointer"
	runtime.SetFinalizer(&i, nil)
	runtime.SetFinalizer((*Tint)(&i), nil)
	runtime.SetFinalizer(Tintptr(&i), nil)

	// check second arg
	runtime.SetFinalizer(&i, NonFunc{})               // want "runtime\\.SetFinalizer: second argument is a\\.NonFunc, not a function"
	runtime.SetFinalizer(&i, func(...interface{}) {}) // want "runtime\\.SetFinalizer: cannot pass \\*int to finalizer func\\(\\.\\.\\.interface{}\\) because dotdotdot"
	runtime.SetFinalizer(&i, func() {})               // want "runtime\\.SetFinalizer: cannot pass \\*int to finalizer func\\(\\)"
	runtime.SetFinalizer(&i, func(*int) {})
	runtime.SetFinalizer(&i, func(*Tint) {}) // want "cannot pass \\*int to finalizer func\\(\\*a\\.Tint\\)"
	runtime.SetFinalizer(&i, func(Tintptr) {})
	runtime.SetFinalizer(&i, func(Tintptr2) {}) // want "cannot pass \\*int to finalizer func\\(a\\.Tintptr2\\)"
	runtime.SetFinalizer(&i, IntFinalizer(nil))
	runtime.SetFinalizer(&i, TintFinalizer(nil)) // want "runtime\\.SetFinalizer: cannot pass \\*int to finalizer a\\.TintFinalizer"
	runtime.SetFinalizer(&i, TintptrFinalizer(nil))
	runtime.SetFinalizer(&i, Tintptr2Finalizer(nil)) // want "runtime\\.SetFinalizer: cannot pass \\*int to finalizer a\\.Tintptr2Finalizer"
	var ti Tint
	runtime.SetFinalizer(&ti, NonFunc{})               // want "runtime\\.SetFinalizer: second argument is a\\.NonFunc, not a function"
	runtime.SetFinalizer(&ti, func(...interface{}) {}) // want "runtime\\.SetFinalizer: cannot pass \\*a\\.Tint to finalizer func\\(\\.\\.\\.interface{}\\) because dotdotdot"
	runtime.SetFinalizer(&ti, func() {})               // want "runtime\\.SetFinalizer: cannot pass \\*a\\.Tint to finalizer func\\(\\)"
	runtime.SetFinalizer(&ti, func(*int) {})           // want "cannot pass \\*a\\.Tint to finalizer func\\(\\*int\\)"
	runtime.SetFinalizer(&ti, func(*Tint) {})
	runtime.SetFinalizer(&ti, func(Tintptr) {}) // want "cannot pass \\*a\\.Tint to finalizer func\\(a\\.Tintptr\\)"
	runtime.SetFinalizer(&ti, func(Tintptr2) {})
	runtime.SetFinalizer(&ti, IntFinalizer(nil)) // want "runtime\\.SetFinalizer: cannot pass \\*a\\.Tint to finalizer a\\.IntFinalizer"
	runtime.SetFinalizer(&ti, TintFinalizer(nil))
	runtime.SetFinalizer(&ti, TintptrFinalizer(nil)) // want "runtime\\.SetFinalizer: cannot pass \\*a\\.Tint to finalizer a\\.TintptrFinalizer"
	runtime.SetFinalizer(&ti, Tintptr2Finalizer(nil))
	tip := Tintptr(&i)
	runtime.SetFinalizer(tip, NonFunc{})               // want "runtime\\.SetFinalizer: second argument is a\\.NonFunc, not a function"
	runtime.SetFinalizer(tip, func(...interface{}) {}) // want "runtime\\.SetFinalizer: cannot pass a\\.Tintptr to finalizer func\\(\\.\\.\\.interface{}\\) because dotdotdot"
	runtime.SetFinalizer(tip, func() {})               // want "runtime\\.SetFinalizer: cannot pass a\\.Tintptr to finalizer func\\(\\)"
	runtime.SetFinalizer(tip, func(*int) {})
	runtime.SetFinalizer(tip, func(*Tint) {}) // want "cannot pass a\\.Tintptr to finalizer func\\(\\*a\\.Tint\\)"
	runtime.SetFinalizer(tip, func(Tintptr) {})
	runtime.SetFinalizer(tip, func(Tintptr2) {}) // want "cannot pass a\\.Tintptr to finalizer func\\(a\\.Tintptr2\\)"
	runtime.SetFinalizer(tip, IntFinalizer(nil))
	runtime.SetFinalizer(tip, TintFinalizer(nil)) // want "runtime\\.SetFinalizer: cannot pass a\\.Tintptr to finalizer a\\.TintFinalizer"
	runtime.SetFinalizer(tip, TintptrFinalizer(nil))
	runtime.SetFinalizer(tip, Tintptr2Finalizer(nil)) // want "runtime\\.SetFinalizer: cannot pass a\\.Tintptr to finalizer a\\.Tintptr2Finalizer"
	tip2 := Tintptr2(&ti)
	runtime.SetFinalizer(tip2, NonFunc{})               // want "runtime\\.SetFinalizer: second argument is a\\.NonFunc, not a function"
	runtime.SetFinalizer(tip2, func(...interface{}) {}) // want "runtime\\.SetFinalizer: cannot pass a\\.Tintptr2 to finalizer func\\(\\.\\.\\.interface{}\\) because dotdotdot"
	runtime.SetFinalizer(tip2, func() {})               // want "runtime\\.SetFinalizer: cannot pass a\\.Tintptr2 to finalizer func\\(\\)"
	runtime.SetFinalizer(tip2, func(*int) {})           // want "cannot pass a\\.Tintptr2 to finalizer func\\(\\*int\\)"
	runtime.SetFinalizer(tip2, func(*Tint) {})
	runtime.SetFinalizer(tip2, func(Tintptr) {}) // want "cannot pass a\\.Tintptr2 to finalizer func\\(a\\.Tintptr\\)"
	runtime.SetFinalizer(tip2, func(Tintptr2) {})
	runtime.SetFinalizer(tip2, IntFinalizer(nil)) // want "runtime\\.SetFinalizer: cannot pass a\\.Tintptr2 to finalizer a\\.IntFinalizer"
	runtime.SetFinalizer(tip2, TintFinalizer(nil))
	runtime.SetFinalizer(tip2, TintptrFinalizer(nil)) // want "runtime\\.SetFinalizer: cannot pass a\\.Tintptr2 to finalizer a\\.TintptrFinalizer"
	runtime.SetFinalizer(tip2, Tintptr2Finalizer(nil))
}
