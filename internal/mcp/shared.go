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
	"strings"
	"time"

	jsonrpc2 "golang.org/x/tools/internal/jsonrpc2_v2"
)

// A MethodHandler handles MCP messages.
// For methods, exactly one of the return values must be nil.
// For notifications, both must be nil.
type MethodHandler[S Session] func(
	ctx context.Context, _ S, method string, params Params) (result Result, err error)

// A methodHandler is a MethodHandler[Session] for some session.
// We need to give up type safety here, or we will end up with a type cycle somewhere
// else. For example, if Session.methodHandler returned a MethodHandler[Session],
// the compiler would complain.
type methodHandler any // MethodHandler[*ClientSession] | MethodHandler[*ServerSession]

// A Session is either a ClientSession or a ServerSession.
type Session interface {
	*ClientSession | *ServerSession
	sendingMethodInfos() map[string]methodInfo
	receivingMethodInfos() map[string]methodInfo
	sendingMethodHandler() methodHandler
	receivingMethodHandler() methodHandler
	getConn() *jsonrpc2.Connection
}

// Middleware is a function from MethodHandlers to MethodHandlers.
type Middleware[S Session] func(MethodHandler[S]) MethodHandler[S]

// addMiddleware wraps the handler in the middleware functions.
func addMiddleware[S Session](handlerp *MethodHandler[S], middleware []Middleware[S]) {
	for _, m := range slices.Backward(middleware) {
		*handlerp = m(*handlerp)
	}
}

func defaultSendingMethodHandler[S Session](ctx context.Context, session S, method string, params Params) (Result, error) {
	info, ok := session.sendingMethodInfos()[method]
	if !ok {
		// This can be called from user code, with an arbitrary value for method.
		return nil, jsonrpc2.ErrNotHandled
	}
	// Notifications don't have results.
	if strings.HasPrefix(method, "notifications/") {
		return nil, session.getConn().Notify(ctx, method, params)
	}
	// Create the result to unmarshal into.
	// The concrete type of the result is the return type of the receiving function.
	res := info.newResult()
	if err := call(ctx, session.getConn(), method, params, res); err != nil {
		return nil, err
	}
	return res, nil
}

func handleNotify[S Session](ctx context.Context, session S, method string, params Params) error {
	mh := session.sendingMethodHandler().(MethodHandler[S])
	_, err := mh(ctx, session, method, params)
	return err
}

func handleSend[R Result, S Session](ctx context.Context, s S, method string, params Params) (R, error) {
	mh := s.sendingMethodHandler().(MethodHandler[S])
	// mh might be user code, so ensure that it returns the right values for the jsonrpc2 protocol.
	res, err := mh(ctx, s, method, params)
	if err != nil {
		var z R
		return z, err
	}
	return res.(R), nil
}

// defaultReceivingMethodHandler is the initial MethodHandler for servers and clients, before being wrapped by middleware.
func defaultReceivingMethodHandler[S Session](ctx context.Context, session S, method string, params Params) (Result, error) {
	info, ok := session.receivingMethodInfos()[method]
	if !ok {
		// This can be called from user code, with an arbitrary value for method.
		return nil, jsonrpc2.ErrNotHandled
	}
	return info.handleMethod.(MethodHandler[S])(ctx, session, method, params)
}

func handleReceive[S Session](ctx context.Context, session S, req *jsonrpc2.Request) (Result, error) {
	info, ok := session.receivingMethodInfos()[req.Method]
	if !ok {
		return nil, jsonrpc2.ErrNotHandled
	}
	params, err := info.unmarshalParams(req.Params)
	if err != nil {
		return nil, fmt.Errorf("handleRequest %q: %w", req.Method, err)
	}

	mh := session.receivingMethodHandler().(MethodHandler[S])
	// mh might be user code, so ensure that it returns the right values for the jsonrpc2 protocol.
	res, err := mh(ctx, session, req.Method, params)
	if err != nil {
		return nil, err
	}
	return res, nil
}

// methodInfo is information about sending and receiving a method.
type methodInfo struct {
	// Unmarshal params from the wire into a Params struct.
	// Used on the receive side.
	unmarshalParams func(json.RawMessage) (Params, error)
	// Run the code when a call to the method is received.
	// Used on the receive side.
	handleMethod methodHandler
	// Create a pointer to a Result struct.
	// Used on the send side.
	newResult func() Result
}

// The following definitions support converting from typed to untyped method handlers.
// Type parameter meanings:
// - S: sessions
// - P: params
// - R: results

// A typedMethodHandler is like a MethodHandler, but with type information.
type typedMethodHandler[S Session, P Params, R Result] func(context.Context, S, P) (R, error)

// newMethodInfo creates a methodInfo from a typedMethodHandler.
func newMethodInfo[S Session, P Params, R Result](d typedMethodHandler[S, P, R]) methodInfo {
	return methodInfo{
		unmarshalParams: func(m json.RawMessage) (Params, error) {
			var p P
			if m != nil {
				if err := json.Unmarshal(m, &p); err != nil {
					return nil, fmt.Errorf("unmarshaling %q into a %T: %w", m, p, err)
				}
			}
			return p, nil
		},
		handleMethod: MethodHandler[S](func(ctx context.Context, session S, _ string, params Params) (Result, error) {
			return d(ctx, session, params.(P))
		}),
		// newResult is used on the send side, to construct the value to unmarshal the result into.
		// R is a pointer to a result struct. There is no way to "unpointer" it without reflection.
		// TODO(jba): explore generic approaches to this, perhaps by treating R in
		// the signature as the unpointered type.
		newResult: func() Result { return reflect.New(reflect.TypeFor[R]().Elem()).Interface().(R) },
	}
}

// serverMethod is glue for creating a typedMethodHandler from a method on Server.
func serverMethod[P Params, R Result](
	f func(*Server, context.Context, *ServerSession, P) (R, error),
) typedMethodHandler[*ServerSession, P, R] {
	return func(ctx context.Context, ss *ServerSession, p P) (R, error) {
		return f(ss.server, ctx, ss, p)
	}
}

// clientMethod is glue for creating a typedMethodHandler from a method on Server.
func clientMethod[P Params, R Result](
	f func(*Client, context.Context, *ClientSession, P) (R, error),
) typedMethodHandler[*ClientSession, P, R] {
	return func(ctx context.Context, cs *ClientSession, p P) (R, error) {
		return f(cs.client, ctx, cs, p)
	}
}

// sessionMethod is glue for creating a typedMethodHandler from a method on ServerSession.
func sessionMethod[S Session, P Params, R Result](f func(S, context.Context, P) (R, error)) typedMethodHandler[S, P, R] {
	return func(ctx context.Context, sess S, p P) (R, error) {
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

func callNotificationHandler[S Session, P any](ctx context.Context, h func(context.Context, S, *P), sess S, params *P) (Result, error) {
	if h != nil {
		h(ctx, sess, params)
	}
	return nil, nil
}

// notifySessions calls Notify on all the sessions.
// Should be called on a copy of the peer sessions.
func notifySessions[S Session](sessions []S, method string, params Params) {
	if sessions == nil {
		return
	}
	// TODO: make this timeout configurable, or call Notify asynchronously.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, s := range sessions {
		if err := handleNotify(ctx, s, method, params); err != nil {
			// TODO(jba): surface this error better
			log.Printf("calling %s: %v", method, err)
		}
	}
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
	if m.ProgressToken == nil && len(m.Data) > 0 {
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

func (*emptyResult) GetMeta() *Meta { panic("should never be called") }

type listParams interface {
	// Returns a pointer to the param's Cursor field.
	cursorPtr() *string
}

type listResult[T any] interface {
	// Returns a pointer to the param's NextCursor field.
	nextCursorPtr() *string
}
