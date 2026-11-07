// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package usedeprecated

import (
	"io/ioutil" // want "\"io/ioutil\" is deprecated: .*"

	"legacy"
)

func x() {
	_, _ = ioutil.ReadFile("") // want "ioutil.ReadFile is deprecated: As of Go 1.16, .*"
	Legacy()                   // expect no deprecation notice.

	x := legacy.Object{} // want "legacy.Object is deprecated: Use obj instead"

	x.DocCommentMethod(1)  // expect no deprecation notice, deprecation is only on interface.
	x.LineCommentMethod(1) // expect no deprecation notice, deprecation is only on interface.

	_ = x.DocCommentField  // want "x.DocCommentField is deprecated: Use `Field` instead."
	_ = x.LineCommentField // want "x.LineCommentField is deprecated: Use `Field` instead."

	// Make sure that the doc comment is chosen over the line comment if both have
	// deprecation tags.
	_ = x.BothCommentField // want "x.BothCommentField is deprecated: Doc comment chosen"

	legacy.Legacy() // want "Legacy is deprecated: use X instead."
	y(x)
}

func y(i legacy.Interface) {
	i.DocCommentMethod(1)  // want "i.DocCommentMethod is deprecated: Use Method instead."
	i.LineCommentMethod(1) // want "i.LineCommentMethod is deprecated: Use Method instead."
}

// Legacy is deprecated.
//
// Deprecated: use X instead.
func Legacy() {} // want Legacy:"Deprecated: use X instead."
