// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package mcp

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"reflect"
	"sync"

	"golang.org/x/tools/internal/mcp/internal/util"
)

func assert(cond bool, msg string) {
	if !cond {
		panic(msg)
	}
}

// Copied from crypto/rand.
// TODO: once 1.24 is assured, just use crypto/rand.
const base32alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567"

func randText() string {
	// ⌈log₃₂ 2¹²⁸⌉ = 26 chars
	src := make([]byte, 26)
	rand.Read(src)
	for i := range src {
		src[i] = base32alphabet[src[i]%32]
	}
	return string(src)
}

// marshalStructWithMap marshals its first argument to JSON, treating the field named
// mapField as an embedded map. The first argument must be a pointer to
// a struct. The underlying type of mapField must be a map[string]any, and it must have
// an "omitempty" json tag.
//
// For example, given this struct:
//
//	type S struct {
//	   A int
//	   Extra map[string] any `json:,omitempty`
//	}
//
// and this value:
//
//	s := S{A: 1, Extra: map[string]any{"B": 2}}
//
// the call marshalJSONWithMap(s, "Extra") would return
//
//	{"A": 1, "B": 2}
//
// It is an error if the map contains the same key as another struct field's
// JSON name.
//
// marshalStructWithMap calls json.Marshal on a value of type T, so T must not
// have a MarshalJSON method that calls this function, on pain of infinite regress.
//
// TODO: avoid this restriction on T by forcing it to marshal in a default way.
// See https://go.dev/play/p/EgXKJHxEx_R.
func marshalStructWithMap[T any](s *T, mapField string) ([]byte, error) {
	// Marshal the struct and the map separately, and concatenate the bytes.
	// This strategy is dramatically less complicated than
	// constructing a synthetic struct or map with the combined keys.
	if s == nil {
		return []byte("null"), nil
	}
	s2 := *s
	vMapField := reflect.ValueOf(&s2).Elem().FieldByName(mapField)
	mapVal := vMapField.Interface().(map[string]any)

	// Check for duplicates.
	names := jsonNames(reflect.TypeFor[T]())
	for key := range mapVal {
		if names[key] {
			return nil, fmt.Errorf("map key %q duplicates struct field", key)
		}
	}

	// Clear the map field, relying on the omitempty tag to omit it.
	vMapField.Set(reflect.Zero(vMapField.Type()))
	structBytes, err := json.Marshal(s2)
	if err != nil {
		return nil, fmt.Errorf("marshalStructWithMap(%+v): %w", s, err)
	}
	if len(mapVal) == 0 {
		return structBytes, nil
	}
	mapBytes, err := json.Marshal(mapVal)
	if err != nil {
		return nil, err
	}
	if len(structBytes) == 2 { // must be "{}"
		return mapBytes, nil
	}
	// "{X}" + "{Y}" => "{X,Y}"
	res := append(structBytes[:len(structBytes)-1], ',')
	res = append(res, mapBytes[1:]...)
	return res, nil
}

// unmarshalStructWithMap is the inverse of marshalStructWithMap.
// T has the same restrictions as in that function.
func unmarshalStructWithMap[T any](data []byte, v *T, mapField string) error {
	// Unmarshal into the struct, ignoring unknown fields.
	if err := json.Unmarshal(data, v); err != nil {
		return err
	}
	// Unmarshal into the map.
	m := map[string]any{}
	if err := json.Unmarshal(data, &m); err != nil {
		return err
	}
	// Delete from the map the fields of the struct.
	for n := range jsonNames(reflect.TypeFor[T]()) {
		delete(m, n)
	}
	if len(m) != 0 {
		reflect.ValueOf(v).Elem().FieldByName(mapField).Set(reflect.ValueOf(m))
	}
	return nil
}

var jsonNamesMap sync.Map // from reflect.Type to map[string]bool

// jsonNames returns the set of JSON object keys that t will marshal into.
// t must be a struct type.
func jsonNames(t reflect.Type) map[string]bool {
	// Lock not necessary: at worst we'll duplicate work.
	if val, ok := jsonNamesMap.Load(t); ok {
		return val.(map[string]bool)
	}
	m := map[string]bool{}
	for i := range t.NumField() {
		info := util.FieldJSONInfo(t.Field(i))
		if !info.Omit {
			m[info.Name] = true
		}
	}
	jsonNamesMap.Store(t, m)
	return m
}
