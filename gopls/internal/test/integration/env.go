// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package integration

import (
	"context"
	"fmt"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/gopls/internal/test/integration/fake"
	"golang.org/x/tools/internal/jsonrpc2/servertest"
	"golang.org/x/tools/internal/mcp"
)

// Env holds the building blocks of an editor testing environment, providing
// wrapper methods that hide the boilerplate of plumbing contexts and checking
// errors.
// Call [Env.Shutdown] for cleaning up resources after the test.
type Env struct {
	TB  testing.TB
	Ctx context.Context

	// Most tests should not need to access the scratch area, editor, server, or
	// connection, but they are available if needed.
	Sandbox *fake.Sandbox
	Server  servertest.Connector

	// Editor is owned by the Env, and shut down
	Editor *fake.Editor

	Awaiter *Awaiter

	// MCPServer, MCPSession and EventChan is owned by the Env, and shut down.
	// Only available if the test enables MCP Server.
	MCPServer  *httptest.Server
	MCPSession *mcp.ClientSession
}

// nextAwaiterRegistration is used to create unique IDs for various Awaiter
// registrations.
var nextAwaiterRegistration atomic.Uint64

// An Awaiter keeps track of relevant LSP state, so that it may be asserted
// upon with Expectations.
//
// Wire it into a fake.Editor using Awaiter.Hooks().
//
// TODO(rfindley): consider simply merging Awaiter with the fake.Editor. It
// probably is not worth its own abstraction.
type Awaiter struct {
	workdir *fake.Workdir

	mu sync.Mutex
	// For simplicity, each waiter gets a unique ID.
	state   State
	waiters map[uint64]*condition

	// collectors map a registration to the collection of messages that have been
	// received since the registration was created.
	docCollectors     map[uint64][]*protocol.ShowDocumentParams
	messageCollectors map[uint64][]*protocol.ShowMessageParams
}

func NewAwaiter(workdir *fake.Workdir) *Awaiter {
	return &Awaiter{
		workdir: workdir,
		state: State{
			diagnostics:   make(map[string]*protocol.PublishDiagnosticsParams),
			work:          make(map[protocol.ProgressToken]*workProgress),
			startedWork:   make(map[string]uint64),
			completedWork: make(map[string]uint64),
		},
		waiters: make(map[uint64]*condition),
	}
}

// Hooks returns LSP client hooks required for awaiting asynchronous expectations.
func (a *Awaiter) Hooks() fake.ClientHooks {
	return fake.ClientHooks{
		OnDiagnostics:            a.onDiagnostics,
		OnLogMessage:             a.onLogMessage,
		OnWorkDoneProgressCreate: a.onWorkDoneProgressCreate,
		OnProgress:               a.onProgress,
		OnShowDocument:           a.onShowDocument,
		OnShowMessage:            a.onShowMessage,
		OnShowMessageRequest:     a.onShowMessageRequest,
		OnRegisterCapability:     a.onRegisterCapability,
		OnUnregisterCapability:   a.onUnregisterCapability,
	}
}

// State encapsulates the server state TODO: explain more
type State struct {
	// diagnostics are a map of relative path->diagnostics params
	diagnostics        map[string]*protocol.PublishDiagnosticsParams
	logs               []*protocol.LogMessageParams
	showDocument       []*protocol.ShowDocumentParams
	showMessage        []*protocol.ShowMessageParams
	showMessageRequest []*protocol.ShowMessageRequestParams

	registrations          []*protocol.RegistrationParams
	registeredCapabilities map[string]protocol.Registration
	unregistrations        []*protocol.UnregistrationParams

	// outstandingWork is a map of token->work summary. All tokens are assumed to
	// be string, though the spec allows for numeric tokens as well.
	work          map[protocol.ProgressToken]*workProgress
	startedWork   map[string]uint64 // title -> count of 'begin'
	completedWork map[string]uint64 // title -> count of 'end'
}

type workProgress struct {
	title, msg, endMsg string
	percent            float64
	complete           bool // seen 'end'
}

type awaitResult struct {
	verdict Verdict
	reason  string
}

// A condition is satisfied when its expectation is [Met] or [Unmeetable]. The
// result is sent on the verdict channel.
type condition struct {
	expectation Expectation
	verdict     chan awaitResult
}

func (a *Awaiter) onDiagnostics(_ context.Context, d *protocol.PublishDiagnosticsParams) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	pth := a.workdir.URIToPath(d.URI)
	a.state.diagnostics[pth] = d
	a.checkConditionsLocked()
	return nil
}

func (a *Awaiter) onShowDocument(_ context.Context, params *protocol.ShowDocumentParams) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	// Update any outstanding listeners.
	for id, s := range a.docCollectors {
		a.docCollectors[id] = append(s, params)
	}

	a.state.showDocument = append(a.state.showDocument, params)
	a.checkConditionsLocked()
	return nil
}

// ListenToShownDocuments registers a listener to incoming showDocument
// notifications. Call the resulting func to deregister the listener and
// receive all notifications that have occurred since the listener was
// registered.
func (a *Awaiter) ListenToShownDocuments() func() []*protocol.ShowDocumentParams {
	id := nextAwaiterRegistration.Add(1)

	a.mu.Lock()
	defer a.mu.Unlock()

	if a.docCollectors == nil {
		a.docCollectors = make(map[uint64][]*protocol.ShowDocumentParams)
	}
	a.docCollectors[id] = nil

	return func() []*protocol.ShowDocumentParams {
		a.mu.Lock()
		defer a.mu.Unlock()
		params := a.docCollectors[id]
		delete(a.docCollectors, id)
		return params
	}
}

func (a *Awaiter) onShowMessage(_ context.Context, params *protocol.ShowMessageParams) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	// Update any outstanding listeners.
	for id, s := range a.messageCollectors {
		a.messageCollectors[id] = append(s, params)
	}

	a.state.showMessage = append(a.state.showMessage, params)
	a.checkConditionsLocked()
	return nil
}

// ListenToShownMessages registers a listener to incoming showMessage
// notifications. Call the resulting func to deregister the listener and
// receive all notifications that have occurred since the listener was
// registered.
//
// ListenToShownMessages should be called before the operation that
// generates the showMessage event to ensure that the event is
// reliably collected.
func (a *Awaiter) ListenToShownMessages() func() []*protocol.ShowMessageParams {
	id := nextAwaiterRegistration.Add(1)

	a.mu.Lock()
	defer a.mu.Unlock()

	if a.messageCollectors == nil {
		a.messageCollectors = make(map[uint64][]*protocol.ShowMessageParams)
	}
	a.messageCollectors[id] = nil

	return func() []*protocol.ShowMessageParams {
		a.mu.Lock()
		defer a.mu.Unlock()
		params := a.messageCollectors[id]
		delete(a.messageCollectors, id)
		return params
	}
}

func (a *Awaiter) onShowMessageRequest(_ context.Context, m *protocol.ShowMessageRequestParams) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.state.showMessageRequest = append(a.state.showMessageRequest, m)
	a.checkConditionsLocked()
	return nil
}

func (a *Awaiter) onLogMessage(_ context.Context, m *protocol.LogMessageParams) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.state.logs = append(a.state.logs, m)
	a.checkConditionsLocked()
	return nil
}

func (a *Awaiter) onWorkDoneProgressCreate(_ context.Context, m *protocol.WorkDoneProgressCreateParams) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.state.work[m.Token] = &workProgress{}
	return nil
}

func (a *Awaiter) onProgress(_ context.Context, m *protocol.ProgressParams) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	work, ok := a.state.work[m.Token]
	if !ok {
		panic(fmt.Sprintf("got progress report for unknown report %v: %v", m.Token, m))
	}
	v := m.Value.(map[string]any)
	switch kind := v["kind"]; kind {
	case "begin":
		work.title = v["title"].(string)
		a.state.startedWork[work.title]++
		if msg, ok := v["message"]; ok {
			work.msg = msg.(string)
		}
	case "report":
		if pct, ok := v["percentage"]; ok {
			work.percent = pct.(float64)
		}
		if msg, ok := v["message"]; ok {
			work.msg = msg.(string)
		}
	case "end":
		work.complete = true
		a.state.completedWork[work.title]++
		if msg, ok := v["message"]; ok {
			work.endMsg = msg.(string)
		}
	}
	a.checkConditionsLocked()
	return nil
}

func (a *Awaiter) onRegisterCapability(_ context.Context, m *protocol.RegistrationParams) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.state.registrations = append(a.state.registrations, m)
	if a.state.registeredCapabilities == nil {
		a.state.registeredCapabilities = make(map[string]protocol.Registration)
	}
	for _, reg := range m.Registrations {
		a.state.registeredCapabilities[reg.Method] = reg
	}
	a.checkConditionsLocked()
	return nil
}

func (a *Awaiter) onUnregisterCapability(_ context.Context, m *protocol.UnregistrationParams) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.state.unregistrations = append(a.state.unregistrations, m)
	a.checkConditionsLocked()
	return nil
}

func (a *Awaiter) checkConditionsLocked() {
	for id, condition := range a.waiters {
		if v, why := condition.expectation.Check(a.state); v != Unmet {
			delete(a.waiters, id)
			condition.verdict <- awaitResult{v, why}
		}
	}
}

// Await blocks until the given expectations are all simultaneously met.
//
// Generally speaking Await should be avoided because it blocks indefinitely if
// gopls ends up in a state where the expectations are never going to be met.
// Use AfterChange or OnceMet instead, so that the runner knows when to stop
// waiting.
func (e *Env) Await(expectations ...Expectation) {
	e.TB.Helper()
	if err := e.Awaiter.Await(e.Ctx, AllOf(expectations...)); err != nil {
		e.TB.Fatal(err)
	}
}

// OnceMet blocks until the precondition is met by the state or becomes
// unmeetable. If it was met, OnceMet checks that the state meets all
// expectations in mustMeets.
func (e *Env) OnceMet(pre Expectation, mustMeets ...Expectation) {
	e.TB.Helper()
	e.Await(OnceMet(pre, AllOf(mustMeets...)))
}

// Await waits for all expectations to simultaneously be met. It should only be
// called from the main test goroutine.
func (a *Awaiter) Await(ctx context.Context, expectation Expectation) error {
	a.mu.Lock()
	// Before adding the waiter, we check if the condition is currently met or
	// failed to avoid a race where the condition was realized before Await was
	// called.
	switch verdict, why := expectation.Check(a.state); verdict {
	case Met:
		a.mu.Unlock()
		return nil
	case Unmeetable:
		err := fmt.Errorf("unmeetable expectation:\n%s\nreason:\n%s", indent(expectation.Description), indent(why))
		a.mu.Unlock()
		return err
	}
	cond := &condition{
		expectation: expectation,
		verdict:     make(chan awaitResult),
	}
	a.waiters[nextAwaiterRegistration.Add(1)] = cond
	a.mu.Unlock()

	var err error
	select {
	case <-ctx.Done():
		err = ctx.Err()
	case res := <-cond.verdict:
		if res.verdict != Met {
			err = fmt.Errorf("the following condition is %s:\n%s\nreason:\n%s",
				res.verdict, indent(expectation.Description), indent(res.reason))
		}
	}
	return err
}

// indent indents all lines of msg, including the first.
func indent(msg string) string {
	const prefix = "  "
	return prefix + strings.ReplaceAll(msg, "\n", "\n"+prefix)
}

// CleanModCache cleans the specified GOMODCACHE.
//
// TODO(golang/go#74595): this is only necessary as the module cache cleaning of the
// sandbox does not respect GOMODCACHE set via EnvVars. We should fix this, but
// that is probably part of a larger refactoring of the sandbox that I'm not
// inclined to undertake. --rfindley.
//
// (For similar problems caused by the same bug, see Test_issue38211; see also
// comment in Sandbox.Env.)
func CleanModCache(t *testing.T, modcache string) {
	cmd := exec.Command("go", "clean", "-modcache")
	cmd.Env = append(os.Environ(), "GOMODCACHE="+modcache, "GOTOOLCHAIN=local")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Errorf("cleaning modcache: %v\noutput:\n%s", err, string(output))
	}
}

// CodeActionByKind returns the first action of (exactly) the specified kind, or an error.
func CodeActionByKind(actions []protocol.CodeAction, kind protocol.CodeActionKind) (*protocol.CodeAction, error) {
	for _, act := range actions {
		if act.Kind == kind {
			return &act, nil
		}
	}
	return nil, fmt.Errorf("can't find action with kind %s, only %#v", kind, actions)
}
