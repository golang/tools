// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package b

type TypeB int

const (
	TypeBOne TypeB = iota
	TypeBTwo
	TypeBThree
)

type ExportedInterface interface {
	isExportedInterface()
}

type notExportedType struct{}

func (notExportedType) isExportedInterface() {}
