// Copyright 2022 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package a

// Misplaced, above imports.
//go:embed embedText // want "go:embed directives must precede a \"var\" declaration"

import (
	"embed"
	embedPkg "embed"
	"fmt"

	_ "embed"
)

//go:embed embedText // ok
var e1 string

// The analyzer does not check for many directives using the same var.
//
//go:embed embedText // ok
//go:embed embedText // ok
var e2 string

// Comments and blank lines between are OK. All types OK.
//
//go:embed embedText // ok
//
// foo

var e3 string

//go:embed embedText //ok
var e4 []byte

//go:embed embedText //ok
var e5 embed.FS

// Followed by wrong kind of decl.
//
//go:embed embedText // want "go:embed directives must precede a \"var\" declaration"
func fooFunc() {}

// Multiple variable specs.
//
//go:embed embedText // want "declarations following go:embed directives must define a single variable"
var e6, e7 []byte

// Specifying a value is not allowed.
//
//go:embed embedText // want "declarations following go:embed directives must not specify a value"
var e8 string = "foo"

// TODO: This should not be OK, misplaced according to compiler.
//
//go:embed embedText // ok
var (
	e9  string
	e10 string
)

// Type definition.
type fooType []byte

//go:embed embedText //ok
var e11 fooType

// Type alias.
type barType = string

//go:embed embedText //ok
var e12 barType

// Renamed embed package.

//go:embed embedText //ok
var e13 embedPkg.FS

// Renamed embed package alias.
type embedAlias = embedPkg.FS

//go:embed embedText //ok
var e14 embedAlias

// var blocks are OK as long as the variable following the directive is OK.
var (
	x, y, z string
	//go:embed embedText // ok
	e20     string
	q, r, t string
)

//go:embed embedText // want "go:embed directives must precede a \"var\" declaration"
var ()

// Incorrect types.

//go:embed embedText // want `declarations following go:embed directives must be of type string, \[\]byte or embed.FS`
var e16 byte

//go:embed embedText // want `declarations following go:embed directives must be of type string, \[\]byte or embed.FS`
var e17 []string

//go:embed embedText // want `declarations following go:embed directives must be of type string, \[\]byte or embed.FS`
var e18 embed.Foo

//go:embed embedText // want `declarations following go:embed directives must be of type string, \[\]byte or embed.FS`
var e19 foo.FS

type byteAlias byte

//go:embed embedText // want `declarations following go:embed directives must be of type string, \[\]byte or embed.FS`
var e15 byteAlias

// A type declaration of embed.FS is not accepted by the compiler, in contrast to an alias.
type embedDecl embed.FS

//go:embed embedText // want `declarations following go:embed directives must be of type string, \[\]byte or embed.FS`
var e16 embedDecl

// This is main function
func main() {
	fmt.Println(s)
}

// No declaration following.
//go:embed embedText // want "go:embed directives must precede a \"var\" declaration"
