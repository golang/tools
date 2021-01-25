// Copyright 2013 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package composite

// DefaultWhitelist is a white list of types in the standard packages
// that are used with unkeyed literals we deem to be acceptable.
var DefaultWhitelist = []string{
	// These image and image/color struct types are frozen. We will never add fields to them.
	"image/color.Alpha16",
	"image/color.Alpha",
	"image/color.CMYK",
	"image/color.Gray16",
	"image/color.Gray",
	"image/color.NRGBA64",
	"image/color.NRGBA",
	"image/color.NYCbCrA",
	"image/color.RGBA64",
	"image/color.RGBA",
	"image/color.YCbCr",
	"image.Point",
	"image.Rectangle",
	"image.Uniform",

	"unicode.Range16",
	"unicode.Range32",

	// These three structs are used in generated test main files,
	// but the generator can be trusted.
	"testing.InternalBenchmark",
	"testing.InternalExample",
	"testing.InternalTest",
}
