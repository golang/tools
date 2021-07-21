// Copyright 2019 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// This file contains tests for the errorsas checker.

package a

import (
	"errors"

	"github.com/lib/errors1"
	"github.com/lib/errors2"
	"github.com/lib/errors3"
	pkgerrors "github.com/pkg/errors"
	xerrors "golang.org/x/xerrors"
)

type myError int

func (myError) Error() string { return "" }

func perr() *error { return nil }

type iface interface {
	m()
}

func two() (error, interface{}) { return nil, nil }

func _() {
	var (
		e  error
		m  myError
		i  int
		f  iface
		ei interface{}
	)
	errors.As(nil, &e)     // *error
	errors.As(nil, &m)     // *T where T implemements error
	errors.As(nil, &f)     // *interface
	errors.As(nil, perr()) // *error, via a call
	errors.As(nil, ei)     //  empty interface

	errors.As(nil, nil) // want `second argument to errors.As must be a non-nil pointer to either a type that implements error, or to any interface type`
	errors.As(nil, e)   // want `second argument to errors.As must be a non-nil pointer to either a type that implements error, or to any interface type`
	errors.As(nil, m)   // want `second argument to errors.As must be a non-nil pointer to either a type that implements error, or to any interface type`
	errors.As(nil, f)   // want `second argument to errors.As must be a non-nil pointer to either a type that implements error, or to any interface type`
	errors.As(nil, &i)  // want `second argument to errors.As must be a non-nil pointer to either a type that implements error, or to any interface type`
	errors.As(two())

	// Check other default packages

	xerrors.As(nil, nil)   // want `second argument to golang.org/x/xerrors.As must be a non-nil pointer to either a type that implements error, or to any interface type`
	pkgerrors.As(nil, nil) // want `second argument to github.com/pkg/errors.As must be a non-nil pointer to either a type that implements error, or to any interface type`

	// Check packages passed via -errorsas.pkgs flag

	errors1.As(nil, nil) // want `second argument to github.com/lib/errors1.As must be a non-nil pointer to either a type that implements error, or to any interface type`
	errors2.As(nil, nil) // want `second argument to github.com/lib/errors2.As must be a non-nil pointer to either a type that implements error, or to any interface type`

	// Ignore package not passed via -errorsas.pkgs flag

	errors3.As(nil, nil)
}
