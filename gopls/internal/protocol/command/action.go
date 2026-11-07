// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package command

import "context"

// An Action is a side effect a command returns instead of performing: talking
// to the client, or changing the workspace. The caller performs it once the
// computation has succeeded, so a command can be run for its result alone
// without taking effect.
//
// The snapshot is gone by then, so an Action must hold values, not the means
// to recompute them.
//
// Examples:
//
//	applyEdits{changes}                     // change the user's files
//	showDocument{uri, &rng}                 // reveal a location
//	showMessage{protocol.Info, msg}         // tell the user something
//	actions{applyEdits{…}, showDocument{…}} // perform in order
//
// A nil Action means the command has no effect.
type Action interface {
	Perform(context.Context) error
}
