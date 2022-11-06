// Copyright 2021 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// This file contains tests for the useany checker.

package a

type Any any

func _[T any]()                    {} // want "could use \"any\" for this empty interface"
func _[X any, T any]()             {} // want "could use \"any\" for this empty interface"
func _[any any]()                  {} // want "could use \"any\" for this empty interface"
func _[T Any]()                    {} // want "could use \"any\" for this empty interface"
func _[T interface{ int | any }]() {} // want "could use \"any\" for this empty interface"
func _[T interface{ int | Any }]() {} // want "could use \"any\" for this empty interface"
func _[T any]()                    {}

type _[T any] int                    // want "could use \"any\" for this empty interface"
type _[X any, T any] int             // want "could use \"any\" for this empty interface"
type _[any any] int                  // want "could use \"any\" for this empty interface"
type _[T Any] int                    // want "could use \"any\" for this empty interface"
type _[T interface{ int | any }] int // want "could use \"any\" for this empty interface"
type _[T interface{ int | Any }] int // want "could use \"any\" for this empty interface"
type _[T any] int
