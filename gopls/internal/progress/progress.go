// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// The progress package defines utilities for reporting the progress
// of long-running operations using features of the LSP client
// interface such as Progress and ShowMessage.
package progress

import (
	"context"
	"fmt"
	"io"
	"math/rand"
	"strconv"
	"strings"
	"sync"

	"golang.org/x/tools/gopls/internal/label"
	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/internal/event"
	"golang.org/x/tools/internal/xcontext"
)

// NewTracker returns a new Tracker that reports progress to the
// specified client.
func NewTracker(client protocol.Client) *Tracker {
	return &Tracker{
		client:     client,
		inProgress: make(map[protocol.ProgressToken]*WorkDone),
	}
}

// A Tracker reports the progress of a long-running operation to an LSP client.
type Tracker struct {
	client                   protocol.Client
	supportsWorkDoneProgress bool

	mu         sync.Mutex
	inProgress map[protocol.ProgressToken]*WorkDone
}

// SetSupportsWorkDoneProgress sets whether the client supports "work done"
// progress reporting. It must be set before using the tracker.
//
// TODO(rfindley): fix this broken initialization pattern.
// Also: do we actually need the fall-back progress behavior using ShowMessage?
// Surely ShowMessage notifications are too noisy to be worthwhile.
func (t *Tracker) SetSupportsWorkDoneProgress(b bool) {
	t.supportsWorkDoneProgress = b
}

// SupportsWorkDoneProgress reports whether the tracker supports work done
// progress reporting.
func (t *Tracker) SupportsWorkDoneProgress() bool {
	return t.supportsWorkDoneProgress
}

// Start notifies the client of work being done on the server. It uses either
// ShowMessage RPCs or $/progress messages, depending on the capabilities of
// the client.  The returned WorkDone handle may be used to report incremental
// progress, and to report work completion. In particular, it is an error to
// call start and not call end(...) on the returned WorkDone handle.
//
// If token is empty, a token will be randomly generated.
//
// The progress item is considered cancellable if the given cancel func is
// non-nil. In this case, cancel is called when the work done
//
// Example:
//
//	func Generate(ctx) (err error) {
//	  ctx, cancel := context.WithCancel(ctx)
//	  defer cancel()
//	  work := s.progress.start(ctx, "generate", "running go generate", cancel)
//	  defer func() {
//	    if err != nil {
//	      work.end(ctx, fmt.Sprintf("generate failed: %v", err))
//	    } else {
//	      work.end(ctx, "done")
//	    }
//	  }()
//	  // Do the work...
//	}
func (t *Tracker) Start(ctx context.Context, title, message string, token protocol.ProgressToken, cancel func()) *WorkDone {
	ctx = xcontext.Detach(ctx) // progress messages should not be cancelled
	wd := &WorkDone{
		client: t.client,
		token:  token,
		cancel: cancel,
	}
	if !t.supportsWorkDoneProgress {
		// Previous iterations of this fallback attempted to retain cancellation
		// support by using ShowMessageCommand with a 'Cancel' button, but this is
		// not ideal as the 'Cancel' dialog stays open even after the command
		// completes.
		//
		// Just show a simple message. Clients can implement workDone progress
		// reporting to get cancellation support.
		if err := wd.client.ShowMessage(ctx, &protocol.ShowMessageParams{
			Type:    protocol.Log,
			Message: message,
		}); err != nil {
			event.Error(ctx, "showing start message for "+title, err)
		}
		return wd
	}
	if wd.token == nil {
		token = strconv.FormatInt(rand.Int63(), 10)
		err := wd.client.WorkDoneProgressCreate(ctx, &protocol.WorkDoneProgressCreateParams{
			Token: token,
		})
		if err != nil {
			wd.err = err
			event.Error(ctx, "starting work for "+title, err)
			return wd
		}
		wd.token = token
	}
	// At this point we have a token that the client knows about. Store the token
	// before starting work.
	t.mu.Lock()
	t.inProgress[wd.token] = wd
	t.mu.Unlock()
	wd.cleanup = func() {
		t.mu.Lock()
		delete(t.inProgress, token)
		t.mu.Unlock()
	}
	err := wd.client.Progress(ctx, &protocol.ProgressParams{
		Token: wd.token,
		Value: &protocol.WorkDoneProgressBegin{
			Kind:        "begin",
			Cancellable: wd.cancel != nil,
			Message:     message,
			Title:       title,
		},
	})
	if err != nil {
		event.Error(ctx, "progress begin", err)
	}
	return wd
}

func (t *Tracker) Cancel(token protocol.ProgressToken) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	wd, ok := t.inProgress[token]
	if !ok {
		return fmt.Errorf("token %q not found in progress", token)
	}
	if wd.cancel == nil {
		return fmt.Errorf("work %q is not cancellable", token)
	}
	wd.doCancel()
	return nil
}

// WorkDone represents a unit of work that is reported to the client via the
// progress API.
type WorkDone struct {
	client protocol.Client
	// If token is nil, this workDone object uses the ShowMessage API, rather
	// than $/progress.
	token protocol.ProgressToken
	// err is set if progress reporting is broken for some reason (for example,
	// if there was an initial error creating a token).
	err error

	cancelMu  sync.Mutex
	cancelled bool
	cancel    func()

	cleanup func()
}

func (wd *WorkDone) Token() protocol.ProgressToken {
	return wd.token
}

func (wd *WorkDone) doCancel() {
	wd.cancelMu.Lock()
	defer wd.cancelMu.Unlock()
	if !wd.cancelled {
		wd.cancel()
	}
}

// Report reports an update on WorkDone report back to the client.
func (wd *WorkDone) Report(ctx context.Context, message string, percentage float64) {
	ctx = xcontext.Detach(ctx) // progress messages should not be cancelled
	if wd == nil {
		return
	}
	wd.cancelMu.Lock()
	cancelled := wd.cancelled
	wd.cancelMu.Unlock()
	if cancelled {
		return
	}
	if wd.err != nil || wd.token == nil {
		// Not using the workDone API, so we do nothing. It would be far too spammy
		// to send incremental messages.
		return
	}
	message = strings.TrimSuffix(message, "\n")
	err := wd.client.Progress(ctx, &protocol.ProgressParams{
		Token: wd.token,
		Value: &protocol.WorkDoneProgressReport{
			Kind: "report",
			// Note that in the LSP spec, the value of Cancellable may be changed to
			// control whether the cancel button in the UI is enabled. Since we don't
			// yet use this feature, the value is kept constant here.
			Cancellable: wd.cancel != nil,
			Message:     message,
			Percentage:  uint32(percentage),
		},
	})
	if err != nil {
		event.Error(ctx, "reporting progress", err)
	}
}

// End reports a workdone completion back to the client.
func (wd *WorkDone) End(ctx context.Context, message string) {
	ctx = xcontext.Detach(ctx) // progress messages should not be cancelled
	if wd == nil {
		return
	}
	var err error
	switch {
	case wd.err != nil:
		// There is a prior error.
	case wd.token == nil:
		// We're falling back to message-based reporting.
		err = wd.client.ShowMessage(ctx, &protocol.ShowMessageParams{
			Type:    protocol.Info,
			Message: message,
		})
	default:
		err = wd.client.Progress(ctx, &protocol.ProgressParams{
			Token: wd.token,
			Value: &protocol.WorkDoneProgressEnd{
				Kind:    "end",
				Message: message,
			},
		})
	}
	if err != nil {
		event.Error(ctx, "ending work", err)
	}
	if wd.cleanup != nil {
		wd.cleanup()
	}
}

// NewEventWriter returns an [io.Writer] that calls the context's
// event printer for each data payload, wrapping it with the
// operation=generate tag to distinguish its logs from others.
func NewEventWriter(ctx context.Context, operation string) io.Writer {
	return &eventWriter{ctx: ctx, operation: operation}
}

type eventWriter struct {
	ctx       context.Context
	operation string
}

func (ew *eventWriter) Write(p []byte) (n int, err error) {
	event.Log(ew.ctx, string(p), label.Operation.Of(ew.operation))
	return len(p), nil
}

// NewWorkDoneWriter wraps a WorkDone handle to provide a Writer interface,
// so that workDone reporting can more easily be hooked into commands.
func NewWorkDoneWriter(ctx context.Context, wd *WorkDone) io.Writer {
	return &workDoneWriter{ctx: ctx, wd: wd}
}

// workDoneWriter wraps a workDone handle to provide a Writer interface,
// so that workDone reporting can more easily be hooked into commands.
type workDoneWriter struct {
	// In order to implement the io.Writer interface, we must close over ctx.
	ctx context.Context
	wd  *WorkDone
}

func (wdw *workDoneWriter) Write(p []byte) (n int, err error) {
	wdw.wd.Report(wdw.ctx, string(p), 0)
	// Don't fail just because of a failure to report progress.
	return len(p), nil
}
