// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package clonetest provides utility functions for testing Clone operations.
//
// The [NonZero] helper may be used to construct a type in which fields are
// recursively set to a non-zero value. This value can then be cloned, and the
// [ZeroOut] helper can set values stored in the clone to zero, recursively.
// Doing so should not mutate the original.
package clonetest

import (
	"fmt"
	"reflect"
)

// NonZero returns a T set to some appropriate nonzero value:
//   - Values of basic type are set to an arbitrary non-zero value.
//   - Struct fields are set to a non-zero value.
//   - Array indices are set to a non-zero value.
//   - Pointers point to a non-zero value.
//   - Maps and slices are given a non-zero element.
//   - Chan, Func, Interface, UnsafePointer are all unsupported.
//
// NonZero breaks cycles by returning a zero value for recursive types.
func NonZero[T any]() T {
	var x T
	t := reflect.TypeOf(x)
	if t == nil {
		panic("untyped nil")
	}
	v := nonZeroValue(t, nil)
	return v.Interface().(T)
}

// nonZeroValue returns a non-zero, addressable value of the given type.
func nonZeroValue(t reflect.Type, seen []reflect.Type) reflect.Value {
	for _, t2 := range seen {
		if t == t2 {
			// Cycle: return the zero value.
			return reflect.Zero(t)
		}
	}
	seen = append(seen, t)
	v := reflect.New(t).Elem()
	switch t.Kind() {
	case reflect.Bool:
		v.SetBool(true)

	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		v.SetInt(1)

	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		v.SetUint(1)

	case reflect.Float32, reflect.Float64:
		v.SetFloat(1)

	case reflect.Complex64, reflect.Complex128:
		v.SetComplex(1)

	case reflect.Array:
		for i := 0; i < v.Len(); i++ {
			v.Index(i).Set(nonZeroValue(t.Elem(), seen))
		}

	case reflect.Map:
		v2 := reflect.MakeMap(t)
		v2.SetMapIndex(nonZeroValue(t.Key(), seen), nonZeroValue(t.Elem(), seen))
		v.Set(v2)

	case reflect.Pointer:
		v2 := nonZeroValue(t.Elem(), seen)
		v.Set(v2.Addr())

	case reflect.Slice:
		v2 := reflect.Append(v, nonZeroValue(t.Elem(), seen))
		v.Set(v2)

	case reflect.String:
		v.SetString(".")

	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			v.Field(i).Set(nonZeroValue(t.Field(i).Type, seen))
		}

	default: // Chan, Func, Interface, UnsafePointer
		panic(fmt.Sprintf("reflect kind %v not supported", t.Kind()))
	}
	return v
}

// ZeroOut recursively sets values contained in t to zero.
// Values of king Chan, Func, Interface, UnsafePointer are all unsupported.
//
// No attempt is made to handle cyclic values.
func ZeroOut[T any](t *T) {
	v := reflect.ValueOf(t).Elem()
	zeroOutValue(v)
}

func zeroOutValue(v reflect.Value) {
	if v.IsZero() {
		return // nothing to do; this also handles untyped nil values
	}

	switch v.Kind() {
	case reflect.Bool,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr,
		reflect.Float32, reflect.Float64,
		reflect.Complex64, reflect.Complex128,
		reflect.String:

		v.Set(reflect.Zero(v.Type()))

	case reflect.Array:
		for i := 0; i < v.Len(); i++ {
			zeroOutValue(v.Index(i))
		}

	case reflect.Map:
		iter := v.MapRange()
		for iter.Next() {
			mv := iter.Value()
			if mv.CanAddr() {
				zeroOutValue(mv)
			} else {
				mv = reflect.New(mv.Type()).Elem()
			}
			v.SetMapIndex(iter.Key(), mv)
		}

	case reflect.Pointer:
		zeroOutValue(v.Elem())

	case reflect.Slice:
		for i := 0; i < v.Len(); i++ {
			zeroOutValue(v.Index(i))
		}

	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			zeroOutValue(v.Field(i))
		}

	default:
		panic(fmt.Sprintf("reflect kind %v not supported", v.Kind()))
	}
}
