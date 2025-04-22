// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package mcp

import (
	"context"
	"io"
	"testing"

	jsonrpc2 "golang.org/x/tools/internal/jsonrpc2_v2"
)

// BatchSize causes a connection to collect n requests or notifications before
// sending a batch on the wire (responses are always sent in isolation).
//
// Exported for testing in the mcp_test package.
func BatchSize(opts *ConnectionOptions, n int) {
	opts.batchSize = n
}

func TestBatchFraming(t *testing.T) {
	// This test checks that the ndjsonFramer can read and write JSON batches.
	//
	// The framer is configured to write a batch size of 2, and we confirm that
	// nothing is sent over the wire until the second write, at which point both
	// messages become available.
	ctx := context.Background()

	r, w := io.Pipe()
	framer := ndjsonFramer{batchSize: 2}
	reader := framer.Reader(r)
	writer := framer.Writer(w)

	// Read the two messages into a channel, for easy testing later.
	read := make(chan jsonrpc2.Message)
	go func() {
		for range 2 {
			msg, _, _ := reader.Read(ctx)
			read <- msg
		}
	}()

	// The first write should not yet be observed by the reader.
	writer.Write(ctx, &jsonrpc2.Request{ID: jsonrpc2.Int64ID(1), Method: "test"})
	select {
	case got := <-read:
		t.Fatalf("after one write, got message %v", got)
	default:
	}

	// ...but the second write causes both messages to be observed.
	writer.Write(ctx, &jsonrpc2.Request{ID: jsonrpc2.Int64ID(2), Method: "test"})
	for _, want := range []int64{1, 2} {
		got := <-read
		if got := got.(*jsonrpc2.Request).ID.Raw(); got != want {
			t.Errorf("got message #%d, want #%d", got, want)
		}
	}
}
