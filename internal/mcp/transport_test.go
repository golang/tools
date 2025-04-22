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

// BatchSize causes a transport to collect n requests or notifications before
// sending a batch on the wire (responses are always sent in isolation).
//
// Exported for testing in the mcp_test package.
func BatchSize(t Transport, n int) {
	if st, ok := t.(*ioStream); ok {
		st.outgoingBatch = make([]jsonrpc2.Message, 0, n)
	}
}

func TestBatchFraming(t *testing.T) {
	// This test checks that the ndjsonFramer can read and write JSON batches.
	//
	// The framer is configured to write a batch size of 2, and we confirm that
	// nothing is sent over the wire until the second write, at which point both
	// messages become available.
	ctx := context.Background()

	r, w := io.Pipe()
	tport := newIOStream(rwc{r, w})
	tport.outgoingBatch = make([]jsonrpc2.Message, 0, 2)

	// Read the two messages into a channel, for easy testing later.
	read := make(chan jsonrpc2.Message)
	go func() {
		for range 2 {
			msg, _, _ := tport.Read(ctx)
			read <- msg
		}
	}()

	// The first write should not yet be observed by the reader.
	tport.Write(ctx, &jsonrpc2.Request{ID: jsonrpc2.Int64ID(1), Method: "test"})
	select {
	case got := <-read:
		t.Fatalf("after one write, got message %v", got)
	default:
	}

	// ...but the second write causes both messages to be observed.
	tport.Write(ctx, &jsonrpc2.Request{ID: jsonrpc2.Int64ID(2), Method: "test"})
	for _, want := range []int64{1, 2} {
		got := <-read
		if got := got.(*jsonrpc2.Request).ID.Raw(); got != want {
			t.Errorf("got message #%d, want #%d", got, want)
		}
	}
}
