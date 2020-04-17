package protocol

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"golang.org/x/tools/internal/jsonrpc2"
)

type loggingStream struct {
	stream jsonrpc2.Stream
	logMu  sync.Mutex
	log    io.Writer
}

// LoggingStream returns a stream that does LSP protocol logging too
func LoggingStream(str jsonrpc2.Stream, w io.Writer) jsonrpc2.Stream {
	return &loggingStream{stream: str, log: w}
}

func (s *loggingStream) Read(ctx context.Context) (jsonrpc2.Message, int64, error) {
	msg, count, err := s.stream.Read(ctx)
	if err == nil {
		s.logMu.Lock()
		defer s.logMu.Unlock()
		logIn(s.log, msg)
	}
	return msg, count, err
}

func (s *loggingStream) Write(ctx context.Context, msg jsonrpc2.Message) (int64, error) {
	s.logMu.Lock()
	defer s.logMu.Unlock()
	logOut(s.log, msg)
	count, err := s.stream.Write(ctx, msg)
	return count, err
}

type req struct {
	method string
	start  time.Time
}

type mapped struct {
	mu          sync.Mutex
	clientCalls map[string]req
	serverCalls map[string]req
}

var maps = &mapped{
	sync.Mutex{},
	make(map[string]req),
	make(map[string]req),
}

// these 4 methods are each used exactly once, but it seemed
// better to have the encapsulation rather than ad hoc mutex
// code in 4 places
func (m *mapped) client(id string, del bool) req {
	m.mu.Lock()
	defer m.mu.Unlock()
	v := m.clientCalls[id]
	if del {
		delete(m.clientCalls, id)
	}
	return v
}

func (m *mapped) server(id string, del bool) req {
	m.mu.Lock()
	defer m.mu.Unlock()
	v := m.serverCalls[id]
	if del {
		delete(m.serverCalls, id)
	}
	return v
}

func (m *mapped) setClient(id string, r req) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.clientCalls[id] = r
}

func (m *mapped) setServer(id string, r req) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.serverCalls[id] = r
}

const eor = "\r\n\r\n\r\n"

func logCommon(outfd io.Writer, msg jsonrpc2.Message, direction, pastTense string) {
	if msg == nil || outfd == nil {
		return
	}
	tm := time.Now()
	tmfmt := tm.Format("15:04:05.000 PM")

	buf := strings.Builder{}
	fmt.Fprintf(&buf, "[Trace - %s] ", tmfmt) // common beginning
	switch msg := msg.(type) {
	case *jsonrpc2.Call:
		id := fmt.Sprint(msg.ID())
		fmt.Fprintf(&buf, "%s request '%s - (%s)'.\n", direction, msg.Method(), id)
		fmt.Fprintf(&buf, "Params: %s%s", msg.Params(), eor)
		maps.setServer(id, req{method: msg.Method(), start: tm})
	case *jsonrpc2.Notification:
		fmt.Fprintf(&buf, "%s notification '%s'.\n", direction, msg.Method())
		fmt.Fprintf(&buf, "Params: %s%s", msg.Params(), eor)
	case *jsonrpc2.Response:
		id := fmt.Sprint(msg.ID())
		if err := msg.Err(); err != nil {
			fmt.Fprintf(outfd, "[Error - %s] %s #%s %s%s", pastTense, tmfmt, id, err, eor)
			return
		}
		cc := maps.client(id, true)
		elapsed := tm.Sub(cc.start)
		fmt.Fprintf(&buf, "Received response '%s - (%s)' in %dms.\n",
			cc.method, id, elapsed/time.Millisecond)
		fmt.Fprintf(&buf, "Result: %s%s", msg.Result(), eor)
	}
	outfd.Write([]byte(buf.String()))
}

// Writing a message to the client, log it
func logOut(outfd io.Writer, msg jsonrpc2.Message) {
	logCommon(outfd, msg, "Received", "Received")
}

// Got a message from the client, log it
func logIn(outfd io.Writer, msg jsonrpc2.Message) {
	logCommon(outfd, msg, "Sending", "Sent")
}
