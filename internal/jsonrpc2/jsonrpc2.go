// Copyright 2018 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package jsonrpc2 is a minimal implementation of the JSON RPC 2 spec.
// https://www.jsonrpc.org/specification
// It is intended to be compatible with other implementations at the wire level.
package jsonrpc2

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"

	"golang.org/x/tools/internal/lsp/debug/tag"
	"golang.org/x/tools/internal/telemetry/event"
)

const (
	// ErrIdleTimeout is returned when serving timed out waiting for new connections.
	ErrIdleTimeout = constError("timed out waiting for new connections")

	// ErrDisconnected signals that the stream or connection exited normally.
	ErrDisconnected = constError("disconnected")
)

// Conn is a JSON RPC 2 client server connection.
// Conn is bidirectional; it does not have a designated server or client end.
type Conn struct {
	seq         int64 // must only be accessed using atomic operations
	LegacyHooks LegacyHooks
	stream      Stream
	pendingMu   sync.Mutex // protects the pending map
	pending     map[ID]chan *WireResponse
	handlingMu  sync.Mutex // protects the handling map
	handling    map[ID]*Request
}

// Request is sent to a server to represent a Call or Notify operaton.
type Request struct {
	conn   *Conn
	cancel context.CancelFunc
	// done holds set of callbacks added by OnReply, and is set back to nil if
	// Reply has been called.
	done []func()

	// The Wire values of the request.
	WireRequest
}

type constError string

func (e constError) Error() string { return string(e) }

// NewErrorf builds a Error struct for the supplied message and code.
// If args is not empty, message and args will be passed to Sprintf.
func NewErrorf(code int64, format string, args ...interface{}) *Error {
	return &Error{
		Code:    code,
		Message: fmt.Sprintf(format, args...),
	}
}

// NewConn creates a new connection object around the supplied stream.
// You must call Run for the connection to be active.
func NewConn(s Stream) *Conn {
	conn := &Conn{
		stream:   s,
		pending:  make(map[ID]chan *WireResponse),
		handling: make(map[ID]*Request),
	}
	return conn
}

// Cancel cancels a pending Call on the server side.
// The call is identified by its id.
// JSON RPC 2 does not specify a cancel message, so cancellation support is not
// directly wired in. This method allows a higher level protocol to choose how
// to propagate the cancel.
func (c *Conn) Cancel(id ID) {
	c.handlingMu.Lock()
	handling, found := c.handling[id]
	c.handlingMu.Unlock()
	if found {
		handling.cancel()
	}
}

// Notify is called to send a notification request over the connection.
// It will return as soon as the notification has been sent, as no response is
// possible.
func (c *Conn) Notify(ctx context.Context, method string, params interface{}) (err error) {
	jsonParams, err := marshalToRaw(params)
	if err != nil {
		return fmt.Errorf("marshalling notify parameters: %v", err)
	}
	request := &WireRequest{
		Method: method,
		Params: jsonParams,
	}
	data, err := json.Marshal(request)
	if err != nil {
		return fmt.Errorf("marshalling notify request: %v", err)
	}
	if c.LegacyHooks != nil {
		ctx = c.LegacyHooks.Request(ctx, c, Send, request)
	}
	ctx, done := event.StartSpan(ctx, request.Method,
		tag.Method.Of(request.Method),
		tag.RPCDirection.Of(tag.Outbound),
		tag.RPCID.Of(request.ID.String()),
	)
	defer func() {
		recordStatus(ctx, err)
		done()
	}()

	event.Record(ctx, tag.Started.Of(1))
	n, err := c.stream.Write(ctx, data)
	event.Record(ctx, tag.ReceivedBytes.Of(n))
	return err
}

// Call sends a request over the connection and then waits for a response.
// If the response is not an error, it will be decoded into result.
// result must be of a type you an pass to json.Unmarshal.
func (c *Conn) Call(ctx context.Context, method string, params, result interface{}) (err error) {
	// generate a new request identifier
	id := ID{Number: atomic.AddInt64(&c.seq, 1)}
	jsonParams, err := marshalToRaw(params)
	if err != nil {
		return fmt.Errorf("marshalling call parameters: %v", err)
	}
	request := &WireRequest{
		ID:     &id,
		Method: method,
		Params: jsonParams,
	}
	// marshal the request now it is complete
	data, err := json.Marshal(request)
	if err != nil {
		return fmt.Errorf("marshalling call request: %v", err)
	}
	if c.LegacyHooks != nil {
		ctx = c.LegacyHooks.Request(ctx, c, Send, request)
	}
	ctx, done := event.StartSpan(ctx, request.Method,
		tag.Method.Of(request.Method),
		tag.RPCDirection.Of(tag.Outbound),
		tag.RPCID.Of(request.ID.String()),
	)
	defer func() {
		recordStatus(ctx, err)
		done()
	}()
	event.Record(ctx, tag.Started.Of(1))
	// We have to add ourselves to the pending map before we send, otherwise we
	// are racing the response. Also add a buffer to rchan, so that if we get a
	// wire response between the time this call is cancelled and id is deleted
	// from c.pending, the send to rchan will not block.
	rchan := make(chan *WireResponse, 1)
	c.pendingMu.Lock()
	c.pending[id] = rchan
	c.pendingMu.Unlock()
	defer func() {
		c.pendingMu.Lock()
		delete(c.pending, id)
		c.pendingMu.Unlock()
	}()
	// now we are ready to send
	n, err := c.stream.Write(ctx, data)
	event.Record(ctx, tag.ReceivedBytes.Of(n))
	if err != nil {
		// sending failed, we will never get a response, so don't leave it pending
		return err
	}
	// now wait for the response
	select {
	case response := <-rchan:
		// is it an error response?
		if response.Error != nil {
			return response.Error
		}
		if result == nil || response.Result == nil {
			return nil
		}
		if err := json.Unmarshal(*response.Result, result); err != nil {
			return fmt.Errorf("unmarshalling result: %v", err)
		}
		return nil
	case <-ctx.Done():
		// Allow the handler to propagate the cancel.
		if c.LegacyHooks != nil {
			c.LegacyHooks.Cancel(ctx, c, id, false)
		}
		return ctx.Err()
	}
}

// Conn returns the connection that created this request.
func (r *Request) Conn() *Conn { return r.conn }

// IsNotify returns true if this request is a notification.
func (r *Request) IsNotify() bool {
	return r.ID == nil
}

// Reply sends a reply to the given request.
// You must call this exactly once for any given request.
// If err is set then result will be ignored.
// This will mark the request as done, triggering any done
// handlers
func (r *Request) Reply(ctx context.Context, result interface{}, err error) error {
	if r.done == nil {
		return fmt.Errorf("reply invoked more than once")
	}

	defer func() {
		recordStatus(ctx, err)
		for i := len(r.done); i > 0; i-- {
			r.done[i-1]()
		}
		r.done = nil
	}()

	if r.IsNotify() {
		return nil
	}

	var raw *json.RawMessage
	if err == nil {
		raw, err = marshalToRaw(result)
	}
	response := &WireResponse{
		Result: raw,
		ID:     r.ID,
	}
	if err != nil {
		if callErr, ok := err.(*Error); ok {
			response.Error = callErr
		} else {
			response.Error = NewErrorf(0, "%s", err)
		}
	}
	data, err := json.Marshal(response)
	if err != nil {
		return err
	}
	n, err := r.conn.stream.Write(ctx, data)
	event.Record(ctx, tag.ReceivedBytes.Of(n))

	if err != nil {
		// TODO(iancottrell): if a stream write fails, we really need to shut down
		// the whole stream
		return err
	}
	return nil
}

func setHandling(r *Request, active bool) {
	if r.ID == nil {
		return
	}
	r.conn.handlingMu.Lock()
	defer r.conn.handlingMu.Unlock()
	if active {
		r.conn.handling[*r.ID] = r
	} else {
		delete(r.conn.handling, *r.ID)
	}
}

// OnReply adds a done callback to the request.
// All added callbacks are invoked during the one required call to Reply, and
// then dropped.
// It is an error to call this after Reply.
// This call is not safe for concurrent use, but should only be invoked by
// handlers and in general only one handler should be working on a request
// at any time.
func (r *Request) OnReply(do func()) {
	r.done = append(r.done, do)
}

// combined has all the fields of both Request and Response.
// We can decode this and then work out which it is.
type combined struct {
	VersionTag VersionTag       `json:"jsonrpc"`
	ID         *ID              `json:"id,omitempty"`
	Method     string           `json:"method"`
	Params     *json.RawMessage `json:"params,omitempty"`
	Result     *json.RawMessage `json:"result,omitempty"`
	Error      *Error           `json:"error,omitempty"`
}

// Run blocks until the connection is terminated, and returns any error that
// caused the termination.
// It must be called exactly once for each Conn.
// It returns only when the reader is closed or there is an error in the stream.
func (c *Conn) Run(runCtx context.Context, handler Handler) error {
	handler = MustReply(handler)
	// we need to make the next request "lock" in an unlocked state to allow
	// the first incoming request to proceed. All later requests are unlocked
	// by the preceding request going to parallel mode.
	nextRequest := make(chan struct{})
	close(nextRequest)
	for {
		// get the data for a message
		data, n, err := c.stream.Read(runCtx)
		if err != nil {
			// The stream failed, we cannot continue. If the client disconnected
			// normally, we should get ErrDisconnected here.
			return err
		}
		// read a combined message
		msg := &combined{}
		if err := json.Unmarshal(data, msg); err != nil {
			// a badly formed message arrived, log it and continue
			// we trust the stream to have isolated the error to just this message
			continue
		}
		// Work out whether this is a request or response.
		switch {
		case msg.Method != "":
			// If method is set it must be a request.
			reqCtx, cancelReq := context.WithCancel(runCtx)
			waitForPrevious := nextRequest
			nextRequest = make(chan struct{})
			unlockNext := nextRequest
			req := &Request{
				conn:   c,
				cancel: cancelReq,
				WireRequest: WireRequest{
					VersionTag: msg.VersionTag,
					Method:     msg.Method,
					Params:     msg.Params,
					ID:         msg.ID,
				},
			}
			req.OnReply(func() {
				close(unlockNext)
			})
			if c.LegacyHooks != nil {
				reqCtx = c.LegacyHooks.Request(reqCtx, c, Receive, &req.WireRequest)
			}
			reqCtx, done := event.StartSpan(reqCtx, req.WireRequest.Method,
				tag.Method.Of(req.WireRequest.Method),
				tag.RPCDirection.Of(tag.Inbound),
				tag.RPCID.Of(req.WireRequest.ID.String()),
			)
			event.Record(reqCtx,
				tag.Started.Of(1),
				tag.SentBytes.Of(n))
			setHandling(req, true)
			_, queueDone := event.StartSpan(reqCtx, "queued")
			go func() {
				<-waitForPrevious
				queueDone()
				defer func() {
					setHandling(req, false)
					done()
					cancelReq()
				}()
				if c.LegacyHooks != nil {
					if c.LegacyHooks.Deliver(reqCtx, req, false) {
						return
					}
				}
				err := handler(reqCtx, req)
				if err != nil {
					// delivery failed, not much we can do
					event.Error(reqCtx, "jsonrpc2 message delivery failed", err)
				}
			}()
		case msg.ID != nil:
			// If method is not set, this should be a response, in which case we must
			// have an id to send the response back to the caller.
			c.pendingMu.Lock()
			rchan, ok := c.pending[*msg.ID]
			c.pendingMu.Unlock()
			if ok {
				response := &WireResponse{
					Result: msg.Result,
					Error:  msg.Error,
					ID:     msg.ID,
				}
				rchan <- response
			}
		default:
		}
	}
}

func marshalToRaw(obj interface{}) (*json.RawMessage, error) {
	data, err := json.Marshal(obj)
	if err != nil {
		return nil, err
	}
	raw := json.RawMessage(data)
	return &raw, nil
}

func recordStatus(ctx context.Context, err error) {
	if err != nil {
		event.Label(ctx, tag.StatusCode.Of("ERROR"))
	} else {
		event.Label(ctx, tag.StatusCode.Of("OK"))
	}
}
