// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package jsonrpc2

import "golang.org/x/tools/internal/event/keys"

// These keys are used for creating labels to instrument jsonrpc2 events.
var (
	Method        = keys.NewString("method", "")
	RPCID         = keys.NewString("id", "")
	RPCDirection  = keys.NewString("direction", "")
	Started       = keys.NewInt("started", "count of started RPCs")
	SentBytes     = keys.NewInt("sent_bytes", "bytes sent")
	ReceivedBytes = keys.NewInt("received_bytes", "bytes received")
	StatusCode    = keys.NewString("status.code", "")
	Latency       = keys.NewFloat("latency", "elapsed time in seconds")
)

const (
	Inbound  = "in"
	Outbound = "out"
)
