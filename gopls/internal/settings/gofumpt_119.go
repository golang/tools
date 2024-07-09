// Copyright 2021 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build !go1.20
// +build !go1.20

package settings

import "context"

const GofumptSupported = false

var GofumptFormat func(ctx context.Context, langVersion, modulePath string, src []byte) ([]byte, error)
