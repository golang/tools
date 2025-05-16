// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// This file contains code shared between client and server, including
// method handler and middleware definitions.
// TODO: much of this is here so that we can factor out commonalities using
// generics. Perhaps it can be simplified with reflection.

package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"

	jsonrpc2 "golang.org/x/tools/internal/jsonrpc2_v2"
)

// A MethodHandler handles MCP messages.
// The params argument is an XXXParams struct pointer, such as *GetPromptParams.
// For methods, a MethodHandler must return either an XXResult struct pointer and a nil error, or
// nil with a non-nil error.
// For notifications, a MethodHandler must return nil, nil.
type MethodHandler[S ClientSession | ServerSession] func(
	ctx context.Context, _ *S, method string, params any) (result any, err error)

// Middleware is a function from MethodHandlers to MethodHandlers.
type Middleware[S ClientSession | ServerSession] func(MethodHandler[S]) MethodHandler[S]

// addMiddleware wraps the handler in the middleware functions.
func addMiddleware[S ClientSession | ServerSession](handlerp *MethodHandler[S], middleware []Middleware[S]) {
	for _, m := range slices.Backward(middleware) {
		*handlerp = m(*handlerp)
	}
}

// session has methods common to both ClientSession and ServerSession.
type session[S ClientSession | ServerSession] interface {
	methodHandler() MethodHandler[S]
	methodInfos() map[string]methodInfo[S]
}

// toSession[S] converts its argument to a session[S].
// Note that since S is constrained to ClientSession | ServerSession, and pointers to those
// types both implement session[S] already, this should be a no-op.
// That it is not, is due (I believe) to a deficency in generics, possibly related to core types.
// TODO(jba): revisit in Go 1.26; perhaps the change in spec due to the removal of core types
// will have resulted by then in a more generous implementation.
func toSession[S ClientSession | ServerSession](sess *S) session[S] {
	return any(sess).(session[S])
}

// defaultMethodHandler is the initial MethodHandler for servers and clients, before being wrapped by middleware.
func defaultMethodHandler[S ClientSession | ServerSession](ctx context.Context, sess *S, method string, params any) (any, error) {
	info, ok := toSession(sess).methodInfos()[method]
	if !ok {
		// This can be called from user code, with an arbitrary value for method.
		return nil, jsonrpc2.ErrNotHandled
	}
	return info.handleMethod(ctx, sess, method, params)
}

func handleRequest[S ClientSession | ServerSession](ctx context.Context, req *jsonrpc2.Request, sess *S) (any, error) {
	info, ok := toSession(sess).methodInfos()[req.Method]
	if !ok {
		return nil, jsonrpc2.ErrNotHandled
	}
	params, err := info.unmarshalParams(req.Params)
	if err != nil {
		return nil, fmt.Errorf("handleRequest %q: %w", req.Method, err)
	}

	// mh might be user code, so ensure that it returns the right values for the jsonrpc2 protocol.
	mh := toSession(sess).methodHandler()
	res, err := mh(ctx, sess, req.Method, params)
	if err != nil {
		return nil, err
	}
	return res, nil
}

// methodInfo is information about invoking a method.
type methodInfo[TSession ClientSession | ServerSession] struct {
	// unmarshal params from the wire into an XXXParams struct
	unmarshalParams func(json.RawMessage) (any, error)
	// run the code for the method
	handleMethod MethodHandler[TSession]
}

// The following definitions support converting from typed to untyped method handlers.
// Type parameter meanings:
// - S: sessions
// - P: params
// - R: results

// A typedMethodHandler is like a MethodHandler, but with type information.
type typedMethodHandler[S, P, R any] func(context.Context, *S, P) (R, error)

// newMethodInfo creates a methodInfo from a typedMethodHandler.
func newMethodInfo[S ClientSession | ServerSession, P, R any](d typedMethodHandler[S, P, R]) methodInfo[S] {
	return methodInfo[S]{
		unmarshalParams: func(m json.RawMessage) (any, error) {
			var p P
			if err := json.Unmarshal(m, &p); err != nil {
				return nil, fmt.Errorf("unmarshaling %q into a %T: %w", m, p, err)
			}
			return p, nil
		},
		handleMethod: func(ctx context.Context, ss *S, _ string, params any) (any, error) {
			return d(ctx, ss, params.(P))
		},
	}
}

// serverMethod is glue for creating a typedMethodHandler from a method on Server.
func serverMethod[P, R any](f func(*Server, context.Context, *ServerSession, P) (R, error)) typedMethodHandler[ServerSession, P, R] {
	return func(ctx context.Context, ss *ServerSession, p P) (R, error) {
		return f(ss.server, ctx, ss, p)
	}
}

// clientMethod is glue for creating a typedMethodHandler from a method on Server.
func clientMethod[P, R any](f func(*Client, context.Context, *ClientSession, P) (R, error)) typedMethodHandler[ClientSession, P, R] {
	return func(ctx context.Context, cs *ClientSession, p P) (R, error) {
		return f(cs.client, ctx, cs, p)
	}
}

// sessionMethod is glue for creating a typedMethodHandler from a method on ServerSession.
func sessionMethod[S ClientSession | ServerSession, P, R any](f func(*S, context.Context, P) (R, error)) typedMethodHandler[S, P, R] {
	return func(ctx context.Context, sess *S, p P) (R, error) {
		return f(sess, ctx, p)
	}
}

// Error codes
const (
	// The error code to return when a resource isn't found.
	// See https://modelcontextprotocol.io/specification/2025-03-26/server/resources#error-handling
	// However, the code they chose in in the wrong space
	// (see https://github.com/modelcontextprotocol/modelcontextprotocol/issues/509).
	// so we pick a different one, arbirarily for now (until they fix it).
	// The immediate problem is that jsonprc2 defines -32002 as "server closing".
	CodeResourceNotFound = -31002
	// The error code if the method exists and was called properly, but the peer does not support it.
	CodeUnsupportedMethod = -31001
)
