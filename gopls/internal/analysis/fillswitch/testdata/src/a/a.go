// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package fillswitch

import (
	data "b"
)

type typeA int

const (
	typeAOne typeA = iota
	typeATwo
	typeAThree
)

func doSwitch() {
	var a typeA
	switch a { // want `Switch has missing cases`
	}

	switch a { // want `Switch has missing cases`
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

	var b data.TypeB
	switch b { // want `Switch has missing cases`
	case data.TypeBOne:
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
	switch not.(type) { // want `Switch has missing cases`
	}

	switch not.(type) { // want `Switch has missing cases`
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
