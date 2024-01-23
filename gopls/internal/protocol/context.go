// Copyright 2019 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package protocol

import (
	"bytes"
	"context"
	"sync"

	"golang.org/x/tools/internal/event"
	"golang.org/x/tools/internal/event/core"
	"golang.org/x/tools/internal/event/export"
	"golang.org/x/tools/internal/event/label"
	"golang.org/x/tools/internal/xcontext"
)

type contextKey int

const (
	clientKey = contextKey(iota)
)

func WithClient(ctx context.Context, client Client) context.Context {
	return context.WithValue(ctx, clientKey, client)
}

func LogEvent(ctx context.Context, ev core.Event, lm label.Map, mt MessageType) context.Context {
	client, ok := ctx.Value(clientKey).(Client)
	if !ok {
		return ctx
	}
	buf := &bytes.Buffer{}
	p := export.Printer{}
	p.WriteEvent(buf, ev, lm)
	msg := &LogMessageParams{Type: mt, Message: buf.String()}
	// Handle messages generated via event.Error, which won't have a level Label.
	if event.IsError(ev) {
		msg.Type = Error
	}

	// The background goroutine lives forever once started,
	// and ensures log messages are sent in order (#61216).
	startLogSenderOnce.Do(func() {
		go func() {
			for f := range logQueue {
				f()
			}
		}()
	})

	// Add the log item to a queue, rather than sending a
	// window/logMessage request to the client synchronously,
	// which would slow down this thread.
	ctx2 := xcontext.Detach(ctx)
	logQueue <- func() { client.LogMessage(ctx2, msg) }

	return ctx
}

var (
	startLogSenderOnce sync.Once
	logQueue           = make(chan func(), 100) // big enough for a large transient burst
)
