// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package c

import "errors"

type SwitchErr struct{}

func (SwitchErr) Error() string { return "" }

func testSwitch(err error) {
	switch err.(type) {
	case SwitchErr: // want "SwitchErr is converted to error both as a value and as a pointer"
	}

	var p *SwitchErr
	var _ error = p // want "SwitchErr is converted to error both as a value and as a pointer"
}

type AsTypeErr struct{}

func (AsTypeErr) Error() string { return "" }

func testAsType(err error) {
	errors.AsType[AsTypeErr](err) // want "AsTypeErr is converted to error both as a value and as a pointer"

	var p *AsTypeErr
	var _ error = p // want "AsTypeErr is converted to error both as a value and as a pointer"
}

type ConsistentSwitchErr struct{} // want ConsistentSwitchErr:`E`

func (ConsistentSwitchErr) Error() string { return "" }

func _(err error) {
	switch err.(type) {
	case ConsistentSwitchErr: // ok, value conversion
	}
}

type ConsistentAsTypeErr struct{} // want ConsistentAsTypeErr:`E`

func (ConsistentAsTypeErr) Error() string { return "" }

func _(err error) {
	errors.AsType[ConsistentAsTypeErr](err) // ok, value conversion
}

type ConsistentSwitchPtrErr struct{ error } // want ConsistentSwitchPtrErr:`\*E`

func _(err error) {
	switch err.(type) {
	case *ConsistentSwitchPtrErr: // ok, pointer conversion
	}
}

type ConsistentAsTypePtrErr struct{ error } // want ConsistentAsTypePtrErr:`\*E`

func _(err error) {
	errors.AsType[*ConsistentAsTypePtrErr](err) // ok, pointer conversion
}
