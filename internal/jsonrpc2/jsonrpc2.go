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
	"time"

	"golang.org/x/tools/internal/lsp/telemetry"
	"golang.org/x/tools/internal/lsp/telemetry/tag"
	"golang.org/x/tools/internal/lsp/telemetry/trace"
)

// Conn is a JSON RPC 2 client server connection.
// Conn is bidirectional; it does not have a designated server or client end.
type Conn struct {
	seq        int64 // must only be accessed using atomic operations
	handlers   []Handler
	stream     Stream
	err        error
	pendingMu  sync.Mutex // protects the pending map
	pending    map[ID]chan *wireResponse
	handlingMu sync.Mutex // protects the handling map
	handling   map[ID]*Request
}

type requestState int

const (
	requestWaiting = requestState(iota)
	requestSerial
	requestParallel
	requestReplied
	requestDone
)

// Request is sent to a server to represent a Call or Notify operaton.
type Request struct {
	conn        *Conn
	cancel      context.CancelFunc
	start       time.Time
	state       requestState
	nextRequest chan struct{}

	// Method is a string containing the method name to invoke.
	Method string
	// Params is either a struct or an array with the parameters of the method.
	Params *json.RawMessage
	// The id of this request, used to tie the response back to the request.
	// Will be either a string or a number. If not set, the request is a notify,
	// and no response is possible.
	ID *ID
}

type rpcStats struct {
	server bool
	method string
	close  func()
	start  time.Time
}

func start(ctx context.Context, server bool, method string, id *ID) (context.Context, *rpcStats) {
	if method == "" {
		panic("no method in rpc stats")
	}
	s := &rpcStats{
		server: server,
		method: method,
		start:  time.Now(),
	}
	mode := telemetry.Outbound
	if server {
		mode = telemetry.Inbound
	}
	ctx, s.close = trace.StartSpan(ctx, method,
		tag.Tag{Key: telemetry.Method, Value: method},
		tag.Tag{Key: telemetry.RPCDirection, Value: mode},
		tag.Tag{Key: telemetry.RPCID, Value: id},
	)
	telemetry.Started.Record(ctx, 1)
	return ctx, s
}

func (s *rpcStats) end(ctx context.Context, err *error) {
	if err != nil && *err != nil {
		ctx = telemetry.StatusCode.With(ctx, "ERROR")
	} else {
		ctx = telemetry.StatusCode.With(ctx, "OK")
	}
	elapsedTime := time.Since(s.start)
	latencyMillis := float64(elapsedTime) / float64(time.Millisecond)
	telemetry.Latency.Record(ctx, latencyMillis)
	s.close()
}

// NewErrorf builds a Error struct for the suppied message and code.
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
		handlers: []Handler{defaultHandler{}},
		stream:   s,
		pending:  make(map[ID]chan *wireResponse),
		handling: make(map[ID]*Request),
	}
	return conn
}

// AddHandler adds a new handler to the set the connection will invoke.
// Handlers are invoked in the reverse order of how they were added, this
// allows the most recent addition to be the first one to attempt to handle a
// message.
func (c *Conn) AddHandler(handler Handler) {
	// prepend the new handlers so we use them first
	c.handlers = append([]Handler{handler}, c.handlers...)
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
	ctx, rpcStats := start(ctx, false, method, nil)
	defer rpcStats.end(ctx, &err)

	jsonParams, err := marshalToRaw(params)
	if err != nil {
		return fmt.Errorf("marshalling notify parameters: %v", err)
	}
	request := &wireRequest{
		Method: method,
		Params: jsonParams,
	}
	data, err := json.Marshal(request)
	if err != nil {
		return fmt.Errorf("marshalling notify request: %v", err)
	}
	for _, h := range c.handlers {
		h.Log(Send, nil, -1, request.Method, request.Params, nil)
	}
	n, err := c.stream.Write(ctx, data)
	telemetry.SentBytes.Record(ctx, n)
	return err
}

// Call sends a request over the connection and then waits for a response.
// If the response is not an error, it will be decoded into result.
// result must be of a type you an pass to json.Unmarshal.
func (c *Conn) Call(ctx context.Context, method string, params, result interface{}) (err error) {
	// generate a new request identifier
	id := ID{Number: atomic.AddInt64(&c.seq, 1)}
	ctx, rpcStats := start(ctx, false, method, &id)
	defer rpcStats.end(ctx, &err)
	jsonParams, err := marshalToRaw(params)
	if err != nil {
		return fmt.Errorf("marshalling call parameters: %v", err)
	}
	request := &wireRequest{
		ID:     &id,
		Method: method,
		Params: jsonParams,
	}
	// marshal the request now it is complete
	data, err := json.Marshal(request)
	if err != nil {
		return fmt.Errorf("marshalling call request: %v", err)
	}
	// we have to add ourselves to the pending map before we send, otherwise we
	// are racing the response
	rchan := make(chan *wireResponse)
	c.pendingMu.Lock()
	c.pending[id] = rchan
	c.pendingMu.Unlock()
	defer func() {
		// clean up the pending response handler on the way out
		c.pendingMu.Lock()
		delete(c.pending, id)
		c.pendingMu.Unlock()
	}()
	// now we are ready to send
	before := time.Now()
	for _, h := range c.handlers {
		h.Log(Send, request.ID, -1, request.Method, request.Params, nil)
	}
	n, err := c.stream.Write(ctx, data)
	telemetry.SentBytes.Record(ctx, n)
	if err != nil {
		// sending failed, we will never get a response, so don't leave it pending
		return err
	}
	// now wait for the response
	select {
	case response := <-rchan:
		elapsed := time.Since(before)
		for _, h := range c.handlers {
			h.Log(Receive, response.ID, elapsed, request.Method, response.Result, response.Error)
		}
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
		// allow the handler to propagate the cancel
		cancelled := false
		for _, h := range c.handlers {
			if h.Cancel(ctx, c, id, cancelled) {
				cancelled = true
			}
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

// Parallel indicates that the system is now allowed to process other requests
// in parallel with this one.
// It is safe to call any number of times, but must only be called from the
// request handling go routine.
// It is implied by both reply and by the handler returning.
func (r *Request) Parallel() {
	if r.state >= requestParallel {
		return
	}
	r.state = requestParallel
	close(r.nextRequest)
}

// Reply sends a reply to the given request.
// It is an error to call this if request was not a call.
// You must call this exactly once for any given request.
// It should only be called from the handler go routine.
// If err is set then result will be ignored.
// If the request has not yet dropped into parallel mode
// it will be before this function returns.
func (r *Request) Reply(ctx context.Context, result interface{}, err error) error {
	if r.state >= requestReplied {
		return fmt.Errorf("reply invoked more than once")
	}
	if r.IsNotify() {
		return fmt.Errorf("reply not invoked with a valid call")
	}
	ctx, close := trace.StartSpan(ctx, r.Method+":reply")
	defer close()

	// reply ends the handling phase of a call, so if we are not yet
	// parallel we should be now. The go routine is allowed to continue
	// to do work after replying, which is why it is important to unlock
	// the rpc system at this point.
	r.Parallel()
	r.state = requestReplied

	elapsed := time.Since(r.start)
	var raw *json.RawMessage
	if err == nil {
		raw, err = marshalToRaw(result)
	}
	response := &wireResponse{
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
	for _, h := range r.conn.handlers {
		h.Log(Send, response.ID, elapsed, r.Method, response.Result, response.Error)
	}
	n, err := r.conn.stream.Write(ctx, data)
	telemetry.SentBytes.Record(ctx, n)

	if err != nil {
		// TODO(iancottrell): if a stream write fails, we really need to shut down
		// the whole stream
		return err
	}
	return nil
}

func (c *Conn) setHandling(r *Request, active bool) {
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
func (c *Conn) Run(ctx context.Context) error {
	// we need to make the next request "lock" in an unlocked state to allow
	// the first incoming request to proceed. All later requests are unlocked
	// by the preceding request going to parallel mode.
	nextRequest := make(chan struct{})
	close(nextRequest)
	for {
		// get the data for a message
		data, n, err := c.stream.Read(ctx)
		if err != nil {
			// the stream failed, we cannot continue
			return err
		}
		// read a combined message
		msg := &combined{}
		if err := json.Unmarshal(data, msg); err != nil {
			// a badly formed message arrived, log it and continue
			// we trust the stream to have isolated the error to just this message
			for _, h := range c.handlers {
				h.Log(Receive, nil, -1, "", nil, NewErrorf(0, "unmarshal failed: %v", err))
			}
			continue
		}
		// work out which kind of message we have
		switch {
		case msg.Method != "":
			// if method is set it must be a request
			reqCtx, cancelReq := context.WithCancel(ctx)
			reqCtx, rpcStats := start(reqCtx, true, msg.Method, msg.ID)
			telemetry.ReceivedBytes.Record(ctx, n)
			thisRequest := nextRequest
			nextRequest = make(chan struct{})
			req := &Request{
				conn:        c,
				cancel:      cancelReq,
				nextRequest: nextRequest,
				start:       time.Now(),
				Method:      msg.Method,
				Params:      msg.Params,
				ID:          msg.ID,
			}
			c.setHandling(req, true)
			go func() {
				<-thisRequest
				req.state = requestSerial
				defer func() {
					c.setHandling(req, false)
					if !req.IsNotify() && req.state < requestReplied {
						req.Reply(reqCtx, nil, NewErrorf(CodeInternalError, "method %q did not reply", req.Method))
					}
					req.Parallel()
					rpcStats.end(reqCtx, nil)
					cancelReq()
				}()
				delivered := false
				for _, h := range c.handlers {
					h.Log(Receive, req.ID, -1, req.Method, req.Params, nil)
					if h.Deliver(reqCtx, req, delivered) {
						delivered = true
					}
				}
			}()
		case msg.ID != nil:
			// we have a response, get the pending entry from the map
			c.pendingMu.Lock()
			rchan := c.pending[*msg.ID]
			if rchan != nil {
				delete(c.pending, *msg.ID)
			}
			c.pendingMu.Unlock()
			// and send the reply to the channel
			response := &wireResponse{
				Result: msg.Result,
				Error:  msg.Error,
				ID:     msg.ID,
			}
			rchan <- response
			close(rchan)
		default:
			for _, h := range c.handlers {
				h.Log(Receive, nil, -1, "", nil, NewErrorf(0, "message not a call, notify or response, ignoring"))
			}
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
