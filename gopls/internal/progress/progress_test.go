// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package progress

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"golang.org/x/tools/gopls/internal/protocol"
)

type fakeClient struct {
	protocol.Client

	token protocol.ProgressToken

	mu                                        sync.Mutex
	created, begun, reported, messages, ended int
}

func (c *fakeClient) checkToken(token protocol.ProgressToken) {
	if token == nil {
		panic("nil token in progress message")
	}
	if c.token != nil && c.token != token {
		panic(fmt.Errorf("invalid token in progress message: got %v, want %v", token, c.token))
	}
}

func (c *fakeClient) WorkDoneProgressCreate(ctx context.Context, params *protocol.WorkDoneProgressCreateParams) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.checkToken(params.Token)
	c.created++
	return nil
}

func (c *fakeClient) Progress(ctx context.Context, params *protocol.ProgressParams) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.checkToken(params.Token)
	switch params.Value.(type) {
	case *protocol.WorkDoneProgressBegin:
		c.begun++
	case *protocol.WorkDoneProgressReport:
		c.reported++
	case *protocol.WorkDoneProgressEnd:
		c.ended++
	default:
		panic(fmt.Errorf("unknown progress value %T", params.Value))
	}
	return nil
}

func (c *fakeClient) ShowMessage(context.Context, *protocol.ShowMessageParams) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.messages++
	return nil
}

func setup() (*Tracker, *fakeClient) {
	c := &fakeClient{}
	tracker := NewTracker(c)
	tracker.SetSupportsWorkDoneProgress(true)
	return tracker, c
}

func TestProgressTracker_Reporting(t *testing.T) {
	for _, test := range []struct {
		name                                            string
		supported                                       bool
		token                                           protocol.ProgressToken
		wantReported, wantCreated, wantBegun, wantEnded int
		wantMessages                                    int
	}{
		{
			name:         "unsupported",
			wantMessages: 2,
		},
		{
			name:         "random token",
			supported:    true,
			wantCreated:  1,
			wantBegun:    1,
			wantReported: 1,
			wantEnded:    1,
		},
		{
			name:         "string token",
			supported:    true,
			token:        "token",
			wantBegun:    1,
			wantReported: 1,
			wantEnded:    1,
		},
		{
			name:         "numeric token",
			supported:    true,
			token:        1,
			wantReported: 1,
			wantBegun:    1,
			wantEnded:    1,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			tracker, client := setup()
			ctx := t.Context()
			tracker.supportsWorkDoneProgress = test.supported
			work := tracker.Start(ctx, "work", "message", test.token, nil)
			client.mu.Lock()
			gotCreated, gotBegun := client.created, client.begun
			client.mu.Unlock()
			if gotCreated != test.wantCreated {
				t.Errorf("got %d created tokens, want %d", gotCreated, test.wantCreated)
			}
			if gotBegun != test.wantBegun {
				t.Errorf("got %d work begun, want %d", gotBegun, test.wantBegun)
			}
			// Ignore errors: this is just testing the reporting behavior.
			work.Report(ctx, "report", 0.5)
			client.mu.Lock()
			gotReported := client.reported
			client.mu.Unlock()
			if gotReported != test.wantReported {
				t.Errorf("got %d progress reports, want %d", gotReported, test.wantCreated)
			}
			work.End(ctx, "done")
			client.mu.Lock()
			gotEnded, gotMessages := client.ended, client.messages
			client.mu.Unlock()
			if gotEnded != test.wantEnded {
				t.Errorf("got %d ended reports, want %d", gotEnded, test.wantEnded)
			}
			if gotMessages != test.wantMessages {
				t.Errorf("got %d messages, want %d", gotMessages, test.wantMessages)
			}
		})
	}
}

func TestProgressTracker_Cancellation(t *testing.T) {
	for _, token := range []protocol.ProgressToken{nil, 1, "a"} {
		tracker, client := setup()
		var cancels int
		cancel := func() { cancels++ }
		ctx := t.Context()
		work := tracker.Start(ctx, "work", "message", token, cancel)
		if err := tracker.Cancel(work.Token()); err != nil {
			t.Fatal(err)
		}
		if err := tracker.Cancel(work.Token()); err != nil {
			t.Fatal(err)
		}
		if cancels != 1 {
			t.Errorf("tracker.Cancel(%v): cancel called %d times, want 1", work.Token(), cancels)
		}
		work.Report(ctx, "after cancel", 0.5)
		// Lock client.mu for consistency with TestProgressTracker_Reporting above
		// (fakeClient.mu guards client.reported), even though this test is single-threaded.
		client.mu.Lock()
		gotReported := client.reported
		client.mu.Unlock()
		if gotReported != 0 {
			t.Errorf("work.Report after Cancel: client.reported = %d, want 0", gotReported)
		}
		work.End(ctx, "done")
	}
}

// TestProgressTracker_ConcurrentSupportsWorkDoneProgress exercises concurrent
// reads and writes of supportsWorkDoneProgress for the race detector (-race).
func TestProgressTracker_ConcurrentSupportsWorkDoneProgress(t *testing.T) {
	tracker, _ := setup()
	ctx := t.Context()
	var wg sync.WaitGroup
	wg.Add(3)
	go func() {
		defer wg.Done()
		for i := range 100 {
			tracker.SetSupportsWorkDoneProgress(i%2 == 0)
		}
	}()
	go func() {
		defer wg.Done()
		for range 100 {
			_ = tracker.SupportsWorkDoneProgress()
		}
	}()
	go func() {
		defer wg.Done()
		for range 100 {
			wd := tracker.Start(ctx, "work", "msg", nil, nil)
			wd.End(ctx, "done")
		}
	}()
	wg.Wait()
}
