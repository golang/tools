// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ref

// Declarations affected by //line directives.

//line foo.go:10
var LineA int

//line bar.go:20:5
type LineT struct{ F int }

/*line baz.go:30:1*/
var LineB int

//line :40
var LineC int
