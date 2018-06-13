// Copyright 2018 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// +build go1.11

package gcimporter

import "go/types"

func newInterface(methods []*types.Func, embeddeds []types.Type) *types.Interface {
	return types.NewInterface2(methods, embeddeds)
}

func embeddedType(iface *types.Interface, i int) types.Type {
	return iface.EmbeddedType(i)
}
