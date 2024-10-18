// Copyright 2019 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package jsonrpc2

import (
	"context"
	"fmt"
	"sync"

	"golang.org/x/tools/internal/event"
)

// Handler is invoked to handle incoming requests.
// The Replier sends a reply to the request and must be called exactly once.
type Handler func(ctx context.Context, reply Replier, req Request) error

// Replier is passed to handlers to allow them to reply to the request.
// If err is set then result will be ignored.
type Replier func(ctx context.Context, result interface{}, err error) error

// MethodNotFound is a Handler that replies to all call requests with the
// standard method not found response.
// This should normally be the final handler in a chain.
func MethodNotFound(ctx context.Context, reply Replier, req Request) error {
	return reply(ctx, nil, fmt.Errorf("%w: %q", ErrMethodNotFound, req.Method()))
}

// MustReplyHandler is a middleware that creates a Handler that panics if the
// wrapped handler does not call Reply for every request that it is passed.
func MustReplyHandler(handler Handler) Handler {
	return func(ctx context.Context, reply Replier, req Request) error {
		called := false
		err := handler(ctx, func(ctx context.Context, result interface{}, err error) error {
			if called {
				panic(fmt.Errorf("request %q replied to more than once", req.Method()))
			}
			called = true
			return reply(ctx, result, err)
		}, req)
		if !called {
			panic(fmt.Errorf("request %q was never replied to", req.Method()))
		}
		return err
	}
}

// CancelHandler returns a handler that supports cancellation, and a function
// that can be used to trigger canceling in progress requests.
func CancelHandler(handler Handler) (Handler, func(id ID)) {
	var mu sync.Mutex
	handling := make(map[ID]context.CancelFunc)
	wrapped := func(ctx context.Context, reply Replier, req Request) error {
		if call, ok := req.(*Call); ok {
			cancelCtx, cancel := context.WithCancel(ctx)
			ctx = cancelCtx
			mu.Lock()
			handling[call.ID()] = cancel
			mu.Unlock()
			innerReply := reply
			reply = func(ctx context.Context, result interface{}, err error) error {
				mu.Lock()
				delete(handling, call.ID())
				mu.Unlock()
				return innerReply(ctx, result, err)
			}
		}
		return handler(ctx, reply, req)
	}
	return wrapped, func(id ID) {
		mu.Lock()
		cancel, found := handling[id]
		mu.Unlock()
		if found {
			cancel()
		}
	}
}

// AsyncHandler is a middleware that returns a handler that processes each
// request goes in its own goroutine.
// The handler returns immediately, without the request being processed.
// Each request then waits for the previous request to finish before it starts.
// This allows the stream to unblock at the cost of unbounded goroutines
// all stalled on the previous one.
func AsyncHandler(handler Handler) Handler {
	nextRequest := make(chan struct{})
	close(nextRequest)
	return func(ctx context.Context, reply Replier, req Request) error {
		waitForPrevious := nextRequest
		nextRequest = make(chan struct{})
		releaser := &releaser{ch: nextRequest}
		innerReply := reply
		reply = func(ctx context.Context, result interface{}, err error) error {
			releaser.release(true)
			return innerReply(ctx, result, err)
		}
		_, queueDone := event.Start(ctx, "queued")
		ctx = context.WithValue(ctx, asyncKey, releaser)
		go func() {
			<-waitForPrevious
			queueDone()
			if err := handler(ctx, reply, req); err != nil {
				event.Error(ctx, "jsonrpc2 async message delivery failed", err)
			}
		}()
		return nil
	}
}

// Async, when used with the [AsyncHandler] middleware, indicates that the
// current jsonrpc2 request may be handled asynchronously to subsequent
// requests.
//
// When not used with an AsyncHandler, Async is a no-op.
//
// Async must be called at most once on each request's context (and its
// descendants).
func Async(ctx context.Context) {
	if r, ok := ctx.Value(asyncKey).(*releaser); ok {
		r.release(false)
	}
}

type asyncKeyType struct{}

var asyncKey = asyncKeyType{}

// A releaser implements concurrency safe 'releasing' of async requests. (A
// request is released when it is allowed to run concurrent with other
// requests, via a call to [Async].)
type releaser struct {
	mu       sync.Mutex
	ch       chan struct{}
	released bool
}

// release closes the associated channel. If soft is set, multiple calls to
// release are allowed.
func (r *releaser) release(soft bool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.released {
		if !soft {
			panic("jsonrpc2.Async called multiple times")
		}
	} else {
		close(r.ch)
		r.released = true
	}
}
