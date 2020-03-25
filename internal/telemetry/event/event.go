// Copyright 2019 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package event provides support for event based telemetry.
package event

import (
	"fmt"
	"time"
)

type eventType uint8

const (
	LogType = eventType(iota)
	StartSpanType
	EndSpanType
	LabelType
	DetachType
	RecordType
)

type Event struct {
	typ     eventType
	At      time.Time
	Message string
	Error   error

	tags []Tag
}

func (e Event) IsLog() bool       { return e.typ == LogType }
func (e Event) IsEndSpan() bool   { return e.typ == EndSpanType }
func (e Event) IsStartSpan() bool { return e.typ == StartSpanType }
func (e Event) IsLabel() bool     { return e.typ == LabelType }
func (e Event) IsDetach() bool    { return e.typ == DetachType }
func (e Event) IsRecord() bool    { return e.typ == RecordType }

func (e Event) Format(f fmt.State, r rune) {
	if !e.At.IsZero() {
		fmt.Fprint(f, e.At.Format("2006/01/02 15:04:05 "))
	}
	fmt.Fprint(f, e.Message)
	if e.Error != nil {
		if f.Flag('+') {
			fmt.Fprintf(f, ": %+v", e.Error)
		} else {
			fmt.Fprintf(f, ": %v", e.Error)
		}
	}
	for it := e.Tags(); it.Valid(); it.Advance() {
		tag := it.Tag()
		fmt.Fprintf(f, "\n\t%s = %v", tag.Key.Name(), tag.Value)
	}
}

func (ev Event) Tags() TagIterator {
	if len(ev.tags) == 0 {
		return TagIterator{}
	}
	return NewTagIterator(ev.tags...)
}

func (ev Event) Map() TagMap {
	return NewTagMap(ev.tags...)
}
