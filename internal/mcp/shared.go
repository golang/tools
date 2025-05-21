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
	"log"
	"reflect"
	"slices"
	"time"

	jsonrpc2 "golang.org/x/tools/internal/jsonrpc2_v2"
)

// A MethodHandler handles MCP messages.
// For methods, exactly one of the return values must be nil.
// For notifications, both must be nil.
type MethodHandler[S ClientSession | ServerSession] func(
	ctx context.Context, _ *S, method string, params Params) (result Result, err error)

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
	getConn() *jsonrpc2.Connection
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
func defaultMethodHandler[S ClientSession | ServerSession](ctx context.Context, sess *S, method string, params Params) (Result, error) {
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
	unmarshalParams func(json.RawMessage) (Params, error)
	// run the code for the method
	handleMethod MethodHandler[TSession]
}

// The following definitions support converting from typed to untyped method handlers.
// Type parameter meanings:
// - S: sessions
// - P: params
// - R: results

// A typedMethodHandler is like a MethodHandler, but with type information.
type typedMethodHandler[S any, P Params, R Result] func(context.Context, *S, P) (R, error)

// newMethodInfo creates a methodInfo from a typedMethodHandler.
func newMethodInfo[S ClientSession | ServerSession, P Params, R Result](d typedMethodHandler[S, P, R]) methodInfo[S] {
	return methodInfo[S]{
		unmarshalParams: func(m json.RawMessage) (Params, error) {
			var p P
			if err := json.Unmarshal(m, &p); err != nil {
				return nil, fmt.Errorf("unmarshaling %q into a %T: %w", m, p, err)
			}
			return p, nil
		},
		handleMethod: func(ctx context.Context, ss *S, _ string, params Params) (Result, error) {
			return d(ctx, ss, params.(P))
		},
	}
}

// serverMethod is glue for creating a typedMethodHandler from a method on Server.
func serverMethod[P Params, R Result](f func(*Server, context.Context, *ServerSession, P) (R, error)) typedMethodHandler[ServerSession, P, R] {
	return func(ctx context.Context, ss *ServerSession, p P) (R, error) {
		return f(ss.server, ctx, ss, p)
	}
}

// clientMethod is glue for creating a typedMethodHandler from a method on Server.
func clientMethod[P Params, R Result](f func(*Client, context.Context, *ClientSession, P) (R, error)) typedMethodHandler[ClientSession, P, R] {
	return func(ctx context.Context, cs *ClientSession, p P) (R, error) {
		return f(cs.client, ctx, cs, p)
	}
}

// sessionMethod is glue for creating a typedMethodHandler from a method on ServerSession.
func sessionMethod[S ClientSession | ServerSession, P Params, R Result](f func(*S, context.Context, P) (R, error)) typedMethodHandler[S, P, R] {
	return func(ctx context.Context, sess *S, p P) (R, error) {
		return f(sess, ctx, p)
	}
}

// Error codes
const (
	// The error code to return when a resource isn't found.
	// See https://modelcontextprotocol.io/specification/2025-03-26/server/resources#error-handling
	// However, the code they chose is in the wrong space
	// (see https://github.com/modelcontextprotocol/modelcontextprotocol/issues/509).
	// so we pick a different one, arbitrarily for now (until they fix it).
	// The immediate problem is that jsonprc2 defines -32002 as "server closing".
	CodeResourceNotFound = -31002
	// The error code if the method exists and was called properly, but the peer does not support it.
	CodeUnsupportedMethod = -31001
)

func callNotificationHandler[S ClientSession | ServerSession, P any](ctx context.Context, h func(context.Context, *S, *P), sess *S, params *P) (Result, error) {
	if h != nil {
		h(ctx, sess, params)
	}
	return nil, nil
}

// notifySessions calls Notify on all the sessions.
// Should be called on a copy of the peer sessions.
func notifySessions[S ClientSession | ServerSession](sessions []*S, method string, params Params) {
	if sessions == nil {
		return
	}
	// TODO: make this timeout configurable, or call Notify asynchronously.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, ss := range sessions {
		if err := toSession(ss).getConn().Notify(ctx, method, params); err != nil {
			// TODO(jba): surface this error better
			log.Printf("calling %s: %v", method, err)
		}
	}
}

func standardCall[TRes, TParams any](ctx context.Context, conn *jsonrpc2.Connection, method string, params TParams) (*TRes, error) {
	var result TRes
	if err := call(ctx, conn, method, params, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

type Meta struct {
	Data map[string]any `json:",omitempty"`
	// For params, the progress token can be nil, a string or an integer.
	// It should be nil for results.
	ProgressToken any `json:"progressToken,omitempty"`
}

type metaSansMethods Meta // avoid infinite recursion during marshaling

func (m Meta) MarshalJSON() ([]byte, error) {
	if p := m.ProgressToken; p != nil {
		if k := reflect.ValueOf(p).Kind(); k != reflect.Int && k != reflect.String {
			return nil, fmt.Errorf("bad type %T for Meta.ProgressToken: must be int or string", p)
		}
	}
	// If ProgressToken is nil, accept Data["progressToken"]. We can't call marshalStructWithMap
	// in that case because it will complain about duplicate fields. (We'd have to
	// make it much smarter to avoid that; not worth it.)
	if m.ProgressToken == nil {
		return json.Marshal(m.Data)
	}
	return marshalStructWithMap((*metaSansMethods)(&m), "Data")
}

func (m *Meta) UnmarshalJSON(data []byte) error {
	return unmarshalStructWithMap(data, (*metaSansMethods)(m), "Data")
}

// Params is a parameter (input) type for an MCP call or notification.
type Params interface {
	// Returns a pointer to the params's Meta field.
	GetMeta() *Meta
}

// Result is a result of an MCP call.
type Result interface {
	// Returns a pointer to the result's Meta field.
	GetMeta() *Meta
}

// emptyResult is returned by methods that have no result, like ping.
// Those methods cannot return nil, because jsonrpc2 cannot handle nils.
type emptyResult struct{}

func (emptyResult) GetMeta() *Meta { panic("should never be called") }
