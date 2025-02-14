package c

// This package is dot-imported by package b.

import "io"

const C = 1

//go:fix inline
type R = map[io.Reader]io.Reader
