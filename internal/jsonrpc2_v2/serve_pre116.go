// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build !go1.16
// +build !go1.16

package jsonrpc2

import (
	"errors"
)

// errClosed is an error with the same string as net.ErrClosed,
// which was added in Go 1.16.
var errClosed = errors.New("use of closed network connection")
