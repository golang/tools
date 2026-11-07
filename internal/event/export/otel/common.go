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
// It returns false if the label has no value.
func labelToAttribute(l label.Label) (otlpAttribute, bool) {
	key := l.Key()
	var v otlpAttributeValue

	switch key := key.(type) {
	case *keys.Int:
		v = otlpAttributeValue{IntValue: varOf(intAsString(key.From(l)))}
	case *keys.Uint:
		v = otlpAttributeValue{IntValue: varOf(intAsString(key.From(l)))}
	case *keys.Float:
		v = otlpAttributeValue{DoubleValue: varOf(key.From(l))}
	case *keys.String:
		v = otlpAttributeValue{StringValue: varOf(key.From(l))}
	case *keys.Error:
		if err := key.From(l); err != nil {
			v = otlpAttributeValue{StringValue: varOf(err.Error())}
		}
	case *keys.Value:
		v = otlpAttributeValue{StringValue: varOf(fmt.Sprint(key.From(l)))}
	default:
		if l.Valid() {
			v = otlpAttributeValue{StringValue: varOf(fmt.Sprint(l))}
		}
	}

	hasValue := v.StringValue != nil || v.IntValue != nil || v.DoubleValue != nil
	return otlpAttribute{Key: key.Name(), Value: v}, hasValue
}

// TODO(adonovan): use go1.26 new(expr)
func varOf[T any](x T) *T { return &x }
