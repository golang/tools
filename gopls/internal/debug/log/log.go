// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package log provides helper methods for exporting log events to the
// internal/event package.
package log

import (
	"context"
	"fmt"

	label1 "golang.org/x/tools/gopls/internal/label"
	"golang.org/x/tools/internal/event"
	"golang.org/x/tools/internal/event/label"
)

// Level parameterizes log severity.
type Level int

const (
	_ Level = iota
	Error
	Warning
	Info
	Debug
	Trace
)

// Log exports a log event labeled with level l.
func (l Level) Log(ctx context.Context, msg string) {
	event.Log(ctx, msg, label1.Level.Of(int(l)))
}

// Logf formats and exports a log event labeled with level l.
func (l Level) Logf(ctx context.Context, format string, args ...interface{}) {
	l.Log(ctx, fmt.Sprintf(format, args...))
}

// LabeledLevel extracts the labeled log l
func LabeledLevel(lm label.Map) Level {
	return Level(label1.Level.Get(lm))
}
