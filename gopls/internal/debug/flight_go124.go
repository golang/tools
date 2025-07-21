// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build !go1.25

package debug

import (
	"errors"
	"net/http"
)

func startFlightRecorder() (http.HandlerFunc, error) {
	return nil, errors.ErrUnsupported
}

func KillTraceViewers() {}
