// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package otel

import (
	"errors"
	"testing"

	"golang.org/x/tools/internal/event/keys"
)

func TestLabelToAttribute_String(t *testing.T) {
	key := keys.NewString("mykey", "")
	l := key.Of("myvalue")

	attr, ok := labelToAttribute(l)
	if !ok {
		t.Fatal("expected ok=true")
	}
	if attr.Key != "mykey" {
		t.Errorf("expected key 'mykey', got %q", attr.Key)
	}
	if attr.Value.StringValue == nil || *attr.Value.StringValue != "myvalue" {
		t.Errorf("expected stringValue 'myvalue', got %v", attr.Value.StringValue)
	}
}

func TestLabelToAttribute_Int(t *testing.T) {
	key := keys.NewInt("count", "")
	l := key.Of(42)

	attr, ok := labelToAttribute(l)
	if !ok {
		t.Fatal("expected ok=true")
	}
	if attr.Key != "count" {
		t.Errorf("expected key 'count', got %q", attr.Key)
	}
	if attr.Value.IntValue == nil || *attr.Value.IntValue != 42 {
		t.Errorf("expected intValue 42, got %v", attr.Value.IntValue)
	}
}

func TestLabelToAttribute_Float(t *testing.T) {
	key := keys.NewFloat("ratio", "")
	l := key.Of(3.14)

	attr, ok := labelToAttribute(l)
	if !ok {
		t.Fatal("expected ok=true")
	}
	if attr.Key != "ratio" {
		t.Errorf("expected key 'ratio', got %q", attr.Key)
	}
	if attr.Value.DoubleValue == nil || *attr.Value.DoubleValue != 3.14 {
		t.Errorf("expected doubleValue 3.14, got %v", attr.Value.DoubleValue)
	}
}

func TestLabelToAttribute_Error(t *testing.T) {
	key := keys.NewError("err", "")
	l := key.Of(errors.New("something failed"))

	attr, ok := labelToAttribute(l)
	if !ok {
		t.Fatal("expected ok=true")
	}
	if attr.Key != "err" {
		t.Errorf("expected key 'err', got %q", attr.Key)
	}
	if attr.Value.StringValue == nil || *attr.Value.StringValue != "something failed" {
		t.Errorf("expected stringValue 'something failed', got %v", attr.Value.StringValue)
	}
}

func TestLabelToAttribute_NilError(t *testing.T) {
	key := keys.NewError("err", "")
	l := key.Of(nil)

	_, ok := labelToAttribute(l)
	if ok {
		t.Error("expected ok=false for nil error")
	}
}
