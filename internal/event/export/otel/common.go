// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package otel

import (
	"fmt"

	"golang.org/x/tools/internal/event/keys"
	"golang.org/x/tools/internal/event/label"
)

// labelToAttribute converts a label to an OTLP attribute.
// Returns false if the label has no value.
func labelToAttribute(l label.Label) (otlpAttribute, bool) {
	key := l.Key()
	var v otlpAttributeValue

	// TODO: use go1.26 new(expr) for pointer assignments below.
	switch key := key.(type) {
	case *keys.Int:
		i := intAsString(key.From(l))
		v = otlpAttributeValue{IntValue: &i}
	case *keys.Int8:
		i := intAsString(key.From(l))
		v = otlpAttributeValue{IntValue: &i}
	case *keys.Int16:
		i := intAsString(key.From(l))
		v = otlpAttributeValue{IntValue: &i}
	case *keys.Int32:
		i := intAsString(key.From(l))
		v = otlpAttributeValue{IntValue: &i}
	case *keys.Int64:
		i := intAsString(key.From(l))
		v = otlpAttributeValue{IntValue: &i}
	case *keys.UInt:
		i := intAsString(key.From(l))
		v = otlpAttributeValue{IntValue: &i}
	case *keys.UInt8:
		i := intAsString(key.From(l))
		v = otlpAttributeValue{IntValue: &i}
	case *keys.UInt16:
		i := intAsString(key.From(l))
		v = otlpAttributeValue{IntValue: &i}
	case *keys.UInt32:
		i := intAsString(key.From(l))
		v = otlpAttributeValue{IntValue: &i}
	case *keys.UInt64:
		i := intAsString(key.From(l))
		v = otlpAttributeValue{IntValue: &i}
	case *keys.Float32:
		f := float64(key.From(l))
		v = otlpAttributeValue{DoubleValue: &f}
	case *keys.Float64:
		f := key.From(l)
		v = otlpAttributeValue{DoubleValue: &f}
	case *keys.Boolean:
		b := key.From(l)
		v = otlpAttributeValue{BoolValue: &b}
	case *keys.String:
		v = otlpAttributeValue{StringValue: strPtr(key.From(l))}
	case *keys.Error:
		if err := key.From(l); err != nil {
			v = otlpAttributeValue{StringValue: strPtr(err.Error())}
		}
	case *keys.Value:
		v = otlpAttributeValue{StringValue: strPtr(fmt.Sprint(key.From(l)))}
	default:
		if l.Valid() {
			v = otlpAttributeValue{StringValue: strPtr(fmt.Sprint(l))}
		}
	}

	hasValue := v.StringValue != nil || v.IntValue != nil ||
		v.DoubleValue != nil || v.BoolValue != nil
	return otlpAttribute{Key: key.Name(), Value: v}, hasValue
}

func strPtr(s string) *string {
	return &s // TODO: use go1.26 new(expr)
}
