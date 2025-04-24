// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package jsonschema

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"reflect"
)

// Equal reports whether two Go values representing JSON values are equal according
// to the JSON Schema spec.
// The values must not contain cycles.
// See https://json-schema.org/draft/2020-12/json-schema-core#section-4.2.2.
// It behaves like reflect.DeepEqual, except that numbers are compared according
// to mathematical equality.
func Equal(x, y any) bool {
	return equalValue(reflect.ValueOf(x), reflect.ValueOf(y))
}

func equalValue(x, y reflect.Value) bool {
	// Copied from src/reflect/deepequal.go, omitting the visited check (because JSON
	// values are trees).
	if !x.IsValid() || !y.IsValid() {
		return x.IsValid() == y.IsValid()
	}

	// Treat numbers specially.
	rx, ok1 := jsonNumber(x)
	ry, ok2 := jsonNumber(y)
	if ok1 && ok2 {
		return rx.Cmp(ry) == 0
	}
	if x.Kind() != y.Kind() {
		return false
	}
	switch x.Kind() {
	case reflect.Array:
		if x.Len() != y.Len() {
			return false
		}
		for i := range x.Len() {
			if !equalValue(x.Index(i), y.Index(i)) {
				return false
			}
		}
		return true
	case reflect.Slice:
		if x.IsNil() != y.IsNil() {
			return false
		}
		if x.Len() != y.Len() {
			return false
		}
		if x.UnsafePointer() == y.UnsafePointer() {
			return true
		}
		// Special case for []byte, which is common.
		if x.Type().Elem().Kind() == reflect.Uint8 && x.Type() == y.Type() {
			return bytes.Equal(x.Bytes(), y.Bytes())
		}
		for i := range x.Len() {
			if !equalValue(x.Index(i), y.Index(i)) {
				return false
			}
		}
		return true
	case reflect.Interface:
		if x.IsNil() || y.IsNil() {
			return x.IsNil() == y.IsNil()
		}
		return equalValue(x.Elem(), y.Elem())
	case reflect.Pointer:
		if x.UnsafePointer() == y.UnsafePointer() {
			return true
		}
		return equalValue(x.Elem(), y.Elem())
	case reflect.Struct:
		t := x.Type()
		if t != y.Type() {
			return false
		}
		for i := range t.NumField() {
			sf := t.Field(i)
			if !sf.IsExported() {
				continue
			}
			if !equalValue(x.FieldByIndex(sf.Index), y.FieldByIndex(sf.Index)) {
				return false
			}
		}
		return true
	case reflect.Map:
		if x.IsNil() != y.IsNil() {
			return false
		}
		if x.Len() != y.Len() {
			return false
		}
		if x.UnsafePointer() == y.UnsafePointer() {
			return true
		}
		iter := x.MapRange()
		for iter.Next() {
			vx := iter.Value()
			vy := y.MapIndex(iter.Key())
			if !vy.IsValid() || !equalValue(vx, vy) {
				return false
			}
		}
		return true
	case reflect.Func:
		if x.Type() != y.Type() {
			return false
		}
		if x.IsNil() && y.IsNil() {
			return true
		}
		panic("cannot compare functions")
	case reflect.String:
		return x.String() == y.String()
	case reflect.Bool:
		return x.Bool() == y.Bool()
	case reflect.Complex64, reflect.Complex128:
		return x.Complex() == y.Complex()
	// Ints, uints and floats handled in jsonNumber, at top of function.
	default:
		panic(fmt.Sprintf("unsupported kind: %s", x.Kind()))
	}
}

// jsonNumber converts a numeric value or a json.Number to a [big.Rat].
// If v is not a number, it returns nil, false.
func jsonNumber(v reflect.Value) (*big.Rat, bool) {
	r := new(big.Rat)
	switch {
	case !v.IsValid():
		return nil, false
	case v.CanInt():
		r.SetInt64(v.Int())
	case v.CanUint():
		r.SetUint64(v.Uint())
	case v.CanFloat():
		r.SetFloat64(v.Float())
	default:
		jn, ok := v.Interface().(json.Number)
		if !ok {
			return nil, false
		}
		if _, ok := r.SetString(jn.String()); !ok {
			// This can fail in rare cases; for example, "1e9999999".
			// That is a valid JSON number, since the spec puts no limit on the size
			// of the exponent.
			return nil, false
		}
	}
	return r, true
}

// jsonType returns a string describing the type of the JSON value,
// as described in the JSON Schema specification:
// https://json-schema.org/draft/2020-12/draft-bhutton-json-schema-validation-01#section-6.1.1.
// It returns "", false if the value is not valid JSON.
func jsonType(v reflect.Value) (string, bool) {
	if !v.IsValid() {
		// Not v.IsNil(): a nil []any is still a JSON array.
		return "null", true
	}
	if v.CanInt() || v.CanUint() {
		return "integer", true
	}
	if v.CanFloat() {
		if _, f := math.Modf(v.Float()); f == 0 {
			return "integer", true
		}
		return "number", true
	}
	switch v.Kind() {
	case reflect.Bool:
		return "boolean", true
	case reflect.String:
		return "string", true
	case reflect.Slice, reflect.Array:
		return "array", true
	case reflect.Map:
		return "object", true
	default:
		return "", false
	}
}

func assert(cond bool, msg string) {
	if !cond {
		panic(msg)
	}
}
