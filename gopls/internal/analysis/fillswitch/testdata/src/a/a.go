// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package fillswitch

import altb "b"

type typeA int

const (
	typeAOne typeA = iota
	typeATwo
	typeAThree
)

func doSwitch() {
	var a typeA
	switch a { // want `Add cases for typeA`
	}

	switch a { // want `Add cases for typeA`
	case typeAOne:
	}

	switch a {
	case typeAOne:
	default:
	}

	switch a {
	case typeAOne:
	case typeATwo:
	case typeAThree:
	}

	var b altb.TypeB
	switch b { // want `Add cases for b.TypeB`
	case altb.TypeBOne:
	}
}

type notification interface {
	isNotification()
}

type notificationOne struct{}

func (notificationOne) isNotification() {}

type notificationTwo struct{}

func (notificationTwo) isNotification() {}

func doTypeSwitch() {
	var not notification
	switch not.(type) { // want `Add cases for notification`
	}

	switch not.(type) { // want `Add cases for notification`
	case notificationOne:
	}

	switch not.(type) {
	case notificationOne:
	case notificationTwo:
	}

	switch not.(type) {
	default:
	}

	var t data.ExportedInterface
	switch t {
	}
}
