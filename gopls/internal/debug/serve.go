// Copyright 2019 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package debug

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"html/template"
	"io"
	stdlog "log"
	"net"
	"net/http"
	"net/http/pprof"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/tools/gopls/internal/cache"
	"golang.org/x/tools/gopls/internal/debug/log"
	label1 "golang.org/x/tools/gopls/internal/label"
	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/gopls/internal/util/bug"
	"golang.org/x/tools/internal/event"
	"golang.org/x/tools/internal/event/core"
	"golang.org/x/tools/internal/event/export"
	"golang.org/x/tools/internal/event/export/metric"
	"golang.org/x/tools/internal/event/export/ocagent"
	"golang.org/x/tools/internal/event/export/prometheus"
	"golang.org/x/tools/internal/event/keys"
	"golang.org/x/tools/internal/event/label"
)

type contextKeyType int

const (
	instanceKey contextKeyType = iota
	traceKey
)

// An Instance holds all debug information associated with a gopls instance.
type Instance struct {
	Logfile       string
	StartTime     time.Time
	ServerAddress string
	OCAgentConfig string

	LogWriter io.Writer

	exporter event.Exporter

	ocagent    *ocagent.Exporter
	prometheus *prometheus.Exporter
	rpcs       *Rpcs
	traces     *traces
	State      *State

	serveMu              sync.Mutex
	debugAddress         string
	listenedDebugAddress string
}

// State holds debugging information related to the server state.
type State struct {
	mu      sync.Mutex
	clients []*Client
	servers []*Server
}

func (st *State) Bugs() []bug.Bug {
	return bug.List()
}

// Caches returns the set of Cache objects currently being served.
func (st *State) Caches() []*cache.Cache {
	var caches []*cache.Cache
	seen := make(map[string]struct{})
	for _, client := range st.Clients() {
		cache := client.Session.Cache()
		if _, found := seen[cache.ID()]; found {
			continue
		}
		seen[cache.ID()] = struct{}{}
		caches = append(caches, cache)
	}
	return caches
}

// Cache returns the Cache that matches the supplied id.
func (st *State) Cache(id string) *cache.Cache {
	for _, c := range st.Caches() {
		if c.ID() == id {
			return c
		}
	}
	return nil
}

// Analysis returns the global Analysis template value.
func (st *State) Analysis() (_ analysisTmpl) { return }

type analysisTmpl struct{}

func (analysisTmpl) AnalyzerRunTimes() []cache.LabelDuration { return cache.AnalyzerRunTimes() }

// Sessions returns the set of Session objects currently being served.
func (st *State) Sessions() []*cache.Session {
	var sessions []*cache.Session
	for _, client := range st.Clients() {
		sessions = append(sessions, client.Session)
	}
	return sessions
}

// Session returns the Session that matches the supplied id.
func (st *State) Session(id string) *cache.Session {
	for _, s := range st.Sessions() {
		if s.ID() == id {
			return s
		}
	}
	return nil
}

// Views returns the set of View objects currently being served.
func (st *State) Views() []*cache.View {
	var views []*cache.View
	for _, s := range st.Sessions() {
		views = append(views, s.Views()...)
	}
	return views
}

// View returns the View that matches the supplied id.
func (st *State) View(id string) *cache.View {
	for _, v := range st.Views() {
		if v.ID() == id {
			return v
		}
	}
	return nil
}

// Clients returns the set of Clients currently being served.
func (st *State) Clients() []*Client {
	st.mu.Lock()
	defer st.mu.Unlock()
	clients := make([]*Client, len(st.clients))
	copy(clients, st.clients)
	return clients
}

// Client returns the Client matching the supplied id.
func (st *State) Client(id string) *Client {
	for _, c := range st.Clients() {
		if c.Session.ID() == id {
			return c
		}
	}
	return nil
}

// Servers returns the set of Servers the instance is currently connected to.
func (st *State) Servers() []*Server {
	st.mu.Lock()
	defer st.mu.Unlock()
	servers := make([]*Server, len(st.servers))
	copy(servers, st.servers)
	return servers
}

// A Client is an incoming connection from a remote client.
type Client struct {
	Session      *cache.Session
	DebugAddress string
	Logfile      string
	GoplsPath    string
	ServerID     string
	Service      protocol.Server
}

// A Server is an outgoing connection to a remote LSP server.
type Server struct {
	ID           string
	DebugAddress string
	Logfile      string
	GoplsPath    string
	ClientID     string
}

// addClient adds a client to the set being served.
func (st *State) addClient(session *cache.Session) {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.clients = append(st.clients, &Client{Session: session})
}

// dropClient removes a client from the set being served.
func (st *State) dropClient(session *cache.Session) {
	st.mu.Lock()
	defer st.mu.Unlock()
	for i, c := range st.clients {
		if c.Session == session {
			copy(st.clients[i:], st.clients[i+1:])
			st.clients[len(st.clients)-1] = nil
			st.clients = st.clients[:len(st.clients)-1]
			return
		}
	}
}

// updateServer updates a server to the set being queried. In practice, there should
// be at most one remote server.
func (st *State) updateServer(server *Server) {
	st.mu.Lock()
	defer st.mu.Unlock()
	for i, existing := range st.servers {
		if existing.ID == server.ID {
			// Replace, rather than mutate, to avoid a race.
			newServers := make([]*Server, len(st.servers))
			copy(newServers, st.servers[:i])
			newServers[i] = server
			copy(newServers[i+1:], st.servers[i+1:])
			st.servers = newServers
			return
		}
	}
	st.servers = append(st.servers, server)
}

// dropServer drops a server from the set being queried.
func (st *State) dropServer(id string) {
	st.mu.Lock()
	defer st.mu.Unlock()
	for i, s := range st.servers {
		if s.ID == id {
			copy(st.servers[i:], st.servers[i+1:])
			st.servers[len(st.servers)-1] = nil
			st.servers = st.servers[:len(st.servers)-1]
			return
		}
	}
}

// an http.ResponseWriter that filters writes
type filterResponse struct {
	w    http.ResponseWriter
	edit func([]byte) []byte
}

func (c filterResponse) Header() http.Header {
	return c.w.Header()
}

func (c filterResponse) Write(buf []byte) (int, error) {
	ans := c.edit(buf)
	return c.w.Write(ans)
}

func (c filterResponse) WriteHeader(n int) {
	c.w.WriteHeader(n)
}

// replace annoying nuls by spaces
func cmdline(w http.ResponseWriter, r *http.Request) {
	fake := filterResponse{
		w: w,
		edit: func(buf []byte) []byte {
			return bytes.ReplaceAll(buf, []byte{0}, []byte{' '})
		},
	}
	pprof.Cmdline(fake, r)
}

func (i *Instance) getCache(r *http.Request) interface{} {
	return i.State.Cache(path.Base(r.URL.Path))
}

func (i *Instance) getAnalysis(r *http.Request) interface{} {
	return i.State.Analysis()
}

func (i *Instance) getSession(r *http.Request) interface{} {
	return i.State.Session(path.Base(r.URL.Path))
}

func (i *Instance) getClient(r *http.Request) interface{} {
	return i.State.Client(path.Base(r.URL.Path))
}

func (i *Instance) getServer(r *http.Request) interface{} {
	i.State.mu.Lock()
	defer i.State.mu.Unlock()
	id := path.Base(r.URL.Path)
	for _, s := range i.State.servers {
		if s.ID == id {
			return s
		}
	}
	return nil
}

func (i *Instance) getFile(r *http.Request) interface{} {
	identifier := path.Base(r.URL.Path)
	sid := path.Base(path.Dir(r.URL.Path))
	s := i.State.Session(sid)
	if s == nil {
		return nil
	}
	for _, o := range s.Overlays() {
		// TODO(adonovan): understand and document this comparison.
		if o.Identity().Hash.String() == identifier {
			return o
		}
	}
	return nil
}

func (i *Instance) getInfo(r *http.Request) interface{} {
	buf := &bytes.Buffer{}
	i.PrintServerInfo(r.Context(), buf)
	return template.HTML(buf.String())
}

func (i *Instance) AddService(s protocol.Server, session *cache.Session) {
	for _, c := range i.State.clients {
		if c.Session == session {
			c.Service = s
			return
		}
	}
	stdlog.Printf("unable to find a Client to add the protocol.Server to")
}

func getMemory(_ *http.Request) interface{} {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return m
}

func init() {
	event.SetExporter(makeGlobalExporter(os.Stderr))
}

func GetInstance(ctx context.Context) *Instance {
	if ctx == nil {
		return nil
	}
	v := ctx.Value(instanceKey)
	if v == nil {
		return nil
	}
	return v.(*Instance)
}

// WithInstance creates debug instance ready for use using the supplied
// configuration and stores it in the returned context.
func WithInstance(ctx context.Context, agent string) context.Context {
	i := &Instance{
		StartTime:     time.Now(),
		OCAgentConfig: agent,
	}
	i.LogWriter = os.Stderr
	ocConfig := ocagent.Discover()
	//TODO: we should not need to adjust the discovered configuration
	ocConfig.Address = i.OCAgentConfig
	i.ocagent = ocagent.Connect(ocConfig)
	i.prometheus = prometheus.New()
	i.rpcs = &Rpcs{}
	i.traces = &traces{}
	i.State = &State{}
	i.exporter = makeInstanceExporter(i)
	return context.WithValue(ctx, instanceKey, i)
}

// SetLogFile sets the logfile for use with this instance.
func (i *Instance) SetLogFile(logfile string, isDaemon bool) (func(), error) {
	// TODO: probably a better solution for deferring closure to the caller would
	// be for the debug instance to itself be closed, but this fixes the
	// immediate bug of logs not being captured.
	closeLog := func() {}
	if logfile != "" {
		if logfile == "auto" {
			if isDaemon {
				logfile = filepath.Join(os.TempDir(), fmt.Sprintf("gopls-daemon-%d.log", os.Getpid()))
			} else {
				logfile = filepath.Join(os.TempDir(), fmt.Sprintf("gopls-%d.log", os.Getpid()))
			}
		}
		f, err := os.Create(logfile)
		if err != nil {
			return nil, fmt.Errorf("unable to create log file: %w", err)
		}
		closeLog = func() {
			defer f.Close()
		}
		stdlog.SetOutput(io.MultiWriter(os.Stderr, f))
		i.LogWriter = f
	}
	i.Logfile = logfile
	return closeLog, nil
}

// Serve starts and runs a debug server in the background on the given addr.
// It also logs the port the server starts on, to allow for :0 auto assigned
// ports.
func (i *Instance) Serve(ctx context.Context, addr string) (string, error) {
	stdlog.SetFlags(stdlog.Lshortfile)
	if addr == "" {
		return "", nil
	}
	i.serveMu.Lock()
	defer i.serveMu.Unlock()

	if i.listenedDebugAddress != "" {
		// Already serving. Return the bound address.
		return i.listenedDebugAddress, nil
	}

	i.debugAddress = addr
	listener, err := net.Listen("tcp", i.debugAddress)
	if err != nil {
		return "", err
	}
	i.listenedDebugAddress = listener.Addr().String()

	port := listener.Addr().(*net.TCPAddr).Port
	if strings.HasSuffix(i.debugAddress, ":0") {
		stdlog.Printf("debug server listening at http://localhost:%d", port)
	}
	event.Log(ctx, "Debug serving", label1.Port.Of(port))
	go func() {
		mux := http.NewServeMux()
		mux.HandleFunc("/", render(MainTmpl, func(*http.Request) interface{} { return i }))
		mux.HandleFunc("/debug/", render(DebugTmpl, nil))
		mux.HandleFunc("/debug/pprof/", pprof.Index)
		mux.HandleFunc("/debug/pprof/cmdline", cmdline)
		mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
		mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
		mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
		if i.prometheus != nil {
			mux.HandleFunc("/metrics/", i.prometheus.Serve)
		}
		if i.rpcs != nil {
			mux.HandleFunc("/rpc/", render(RPCTmpl, i.rpcs.getData))
		}
		if i.traces != nil {
			mux.HandleFunc("/trace/", render(TraceTmpl, i.traces.getData))
		}
		mux.HandleFunc("/analysis/", render(AnalysisTmpl, i.getAnalysis))
		mux.HandleFunc("/cache/", render(CacheTmpl, i.getCache))
		mux.HandleFunc("/session/", render(SessionTmpl, i.getSession))
		mux.HandleFunc("/client/", render(ClientTmpl, i.getClient))
		mux.HandleFunc("/server/", render(ServerTmpl, i.getServer))
		mux.HandleFunc("/file/", render(FileTmpl, i.getFile))
		mux.HandleFunc("/info", render(InfoTmpl, i.getInfo))
		mux.HandleFunc("/memory", render(MemoryTmpl, getMemory))

		// Internal debugging helpers.
		mux.HandleFunc("/gc", func(w http.ResponseWriter, r *http.Request) {
			runtime.GC()
			runtime.GC()
			runtime.GC()
			http.Redirect(w, r, "/memory", http.StatusTemporaryRedirect)
		})
		mux.HandleFunc("/_makeabug", func(w http.ResponseWriter, r *http.Request) {
			bug.Report("bug here")
			http.Error(w, "made a bug", http.StatusOK)
		})

		if err := http.Serve(listener, mux); err != nil {
			event.Error(ctx, "Debug server failed", err)
			return
		}
		event.Log(ctx, "Debug server finished")
	}()
	return i.listenedDebugAddress, nil
}

func (i *Instance) DebugAddress() string {
	i.serveMu.Lock()
	defer i.serveMu.Unlock()
	return i.debugAddress
}

func (i *Instance) ListenedDebugAddress() string {
	i.serveMu.Lock()
	defer i.serveMu.Unlock()
	return i.listenedDebugAddress
}

func makeGlobalExporter(stderr io.Writer) event.Exporter {
	p := export.Printer{}
	var pMu sync.Mutex
	return func(ctx context.Context, ev core.Event, lm label.Map) context.Context {
		i := GetInstance(ctx)

		if event.IsLog(ev) {
			// Don't log context cancellation errors.
			if err := keys.Err.Get(ev); errors.Is(err, context.Canceled) {
				return ctx
			}
			// Make sure any log messages without an instance go to stderr.
			if i == nil {
				pMu.Lock()
				p.WriteEvent(stderr, ev, lm)
				pMu.Unlock()
			}
			level := log.LabeledLevel(lm)
			// Exclude trace logs from LSP logs.
			if level < log.Trace {
				ctx = protocol.LogEvent(ctx, ev, lm, messageType(level))
			}
		}
		if i == nil {
			return ctx
		}
		return i.exporter(ctx, ev, lm)
	}
}

func messageType(l log.Level) protocol.MessageType {
	switch l {
	case log.Error:
		return protocol.Error
	case log.Warning:
		return protocol.Warning
	case log.Debug:
		return protocol.Log
	}
	return protocol.Info
}

func makeInstanceExporter(i *Instance) event.Exporter {
	exporter := func(ctx context.Context, ev core.Event, lm label.Map) context.Context {
		if i.ocagent != nil {
			ctx = i.ocagent.ProcessEvent(ctx, ev, lm)
		}
		if i.prometheus != nil {
			ctx = i.prometheus.ProcessEvent(ctx, ev, lm)
		}
		if i.rpcs != nil {
			ctx = i.rpcs.ProcessEvent(ctx, ev, lm)
		}
		if i.traces != nil {
			ctx = i.traces.ProcessEvent(ctx, ev, lm)
		}
		if event.IsLog(ev) {
			if s := cache.KeyCreateSession.Get(ev); s != nil {
				i.State.addClient(s)
			}
			if sid := label1.NewServer.Get(ev); sid != "" {
				i.State.updateServer(&Server{
					ID:           sid,
					Logfile:      label1.Logfile.Get(ev),
					DebugAddress: label1.DebugAddress.Get(ev),
					GoplsPath:    label1.GoplsPath.Get(ev),
					ClientID:     label1.ClientID.Get(ev),
				})
			}
			if s := cache.KeyShutdownSession.Get(ev); s != nil {
				i.State.dropClient(s)
			}
			if sid := label1.EndServer.Get(ev); sid != "" {
				i.State.dropServer(sid)
			}
			if s := cache.KeyUpdateSession.Get(ev); s != nil {
				if c := i.State.Client(s.ID()); c != nil {
					c.DebugAddress = label1.DebugAddress.Get(ev)
					c.Logfile = label1.Logfile.Get(ev)
					c.ServerID = label1.ServerID.Get(ev)
					c.GoplsPath = label1.GoplsPath.Get(ev)
				}
			}
		}
		return ctx
	}
	// StdTrace must be above export.Spans below (by convention, export
	// middleware applies its wrapped exporter last).
	exporter = StdTrace(exporter)
	metrics := metric.Config{}
	registerMetrics(&metrics)
	exporter = metrics.Exporter(exporter)
	exporter = export.Spans(exporter)
	exporter = export.Labels(exporter)
	return exporter
}

type dataFunc func(*http.Request) interface{}

func render(tmpl *template.Template, fun dataFunc) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		var data interface{}
		if fun != nil {
			data = fun(r)
		}
		if err := tmpl.Execute(w, data); err != nil {
			event.Error(context.Background(), "", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	}
}

func commas(s string) string {
	for i := len(s); i > 3; {
		i -= 3
		s = s[:i] + "," + s[i:]
	}
	return s
}

func fuint64(v uint64) string {
	return commas(strconv.FormatUint(v, 10))
}

func fuint32(v uint32) string {
	return commas(strconv.FormatUint(uint64(v), 10))
}

func fcontent(v []byte) string {
	return string(v)
}

var BaseTemplate = template.Must(template.New("").Parse(`
<html>
<head>
<title>{{template "title" .}}</title>
<style>
.profile-name{
	display:inline-block;
	width:6rem;
}
td.value {
	text-align: right;
}
ul.spans {
	font-family: monospace;
	font-size:   85%;
}
body {
	font-family: sans-serif;
	font-size: 1rem;
	line-height: normal;
}
</style>
{{block "head" .}}{{end}}
</head>
<body>
<a href="/">Main</a>
<a href="/info">Info</a>
<a href="/memory">Memory</a>
<a href="/debug/pprof">Profiling</a>
<a href="/metrics">Metrics</a>
<a href="/rpc">RPC</a>
<a href="/trace">Trace</a>
<a href="/analysis">Analysis</a>
<hr>
<h1>{{template "title" .}}</h1>
{{block "body" .}}
Unknown page
{{end}}
</body>
</html>

{{define "cachelink"}}<a href="/cache/{{.}}">Cache {{.}}</a>{{end}}
{{define "clientlink"}}<a href="/client/{{.}}">Client {{.}}</a>{{end}}
{{define "serverlink"}}<a href="/server/{{.}}">Server {{.}}</a>{{end}}
{{define "sessionlink"}}<a href="/session/{{.}}">Session {{.}}</a>{{end}}
`)).Funcs(template.FuncMap{
	"fuint64":  fuint64,
	"fuint32":  fuint32,
	"fcontent": fcontent,
	"localAddress": func(s string) string {
		// Try to translate loopback addresses to localhost, both for cosmetics and
		// because unspecified ipv6 addresses can break links on Windows.
		//
		// TODO(rfindley): In the future, it would be better not to assume the
		// server is running on localhost, and instead construct this address using
		// the remote host.
		host, port, err := net.SplitHostPort(s)
		if err != nil {
			return s
		}
		ip := net.ParseIP(host)
		if ip == nil {
			return s
		}
		if ip.IsLoopback() || ip.IsUnspecified() {
			return "localhost:" + port
		}
		return s
	},
	// TODO(rfindley): re-enable option inspection.
	// "options": func(s *cache.Session) []sessionOption {
	// 	return showOptions(s.Options())
	// },
})

var MainTmpl = template.Must(template.Must(BaseTemplate.Clone()).Parse(`
{{define "title"}}Gopls server information{{end}}
{{define "body"}}
<h2>Caches</h2>
<ul>{{range .State.Caches}}<li>{{template "cachelink" .ID}}</li>{{end}}</ul>
<h2>Sessions</h2>
<ul>{{range .State.Sessions}}<li>{{template "sessionlink" .ID}} from {{template "cachelink" .Cache.ID}}</li>{{end}}</ul>
<h2>Clients</h2>
<ul>{{range .State.Clients}}<li>{{template "clientlink" .Session.ID}}</li>{{end}}</ul>
<h2>Servers</h2>
<ul>{{range .State.Servers}}<li>{{template "serverlink" .ID}}</li>{{end}}</ul>
<h2>Bug reports</h2>
<dl>{{range .State.Bugs}}<dt>{{.Key}}</dt><dd>{{.Description}}</dd>{{end}}</dl>
{{end}}
`))

var InfoTmpl = template.Must(template.Must(BaseTemplate.Clone()).Parse(`
{{define "title"}}Gopls version information{{end}}
{{define "body"}}
{{.}}
{{end}}
`))

var MemoryTmpl = template.Must(template.Must(BaseTemplate.Clone()).Parse(`
{{define "title"}}Gopls memory usage{{end}}
{{define "head"}}<meta http-equiv="refresh" content="5">{{end}}
{{define "body"}}
<form action="/gc"><input type="submit" value="Run garbage collector"/></form>
<h2>Stats</h2>
<table>
<tr><td class="label">Allocated bytes</td><td class="value">{{fuint64 .HeapAlloc}}</td></tr>
<tr><td class="label">Total allocated bytes</td><td class="value">{{fuint64 .TotalAlloc}}</td></tr>
<tr><td class="label">System bytes</td><td class="value">{{fuint64 .Sys}}</td></tr>
<tr><td class="label">Heap system bytes</td><td class="value">{{fuint64 .HeapSys}}</td></tr>
<tr><td class="label">Malloc calls</td><td class="value">{{fuint64 .Mallocs}}</td></tr>
<tr><td class="label">Frees</td><td class="value">{{fuint64 .Frees}}</td></tr>
<tr><td class="label">Idle heap bytes</td><td class="value">{{fuint64 .HeapIdle}}</td></tr>
<tr><td class="label">In use bytes</td><td class="value">{{fuint64 .HeapInuse}}</td></tr>
<tr><td class="label">Released to system bytes</td><td class="value">{{fuint64 .HeapReleased}}</td></tr>
<tr><td class="label">Heap object count</td><td class="value">{{fuint64 .HeapObjects}}</td></tr>
<tr><td class="label">Stack in use bytes</td><td class="value">{{fuint64 .StackInuse}}</td></tr>
<tr><td class="label">Stack from system bytes</td><td class="value">{{fuint64 .StackSys}}</td></tr>
<tr><td class="label">Bucket hash bytes</td><td class="value">{{fuint64 .BuckHashSys}}</td></tr>
<tr><td class="label">GC metadata bytes</td><td class="value">{{fuint64 .GCSys}}</td></tr>
<tr><td class="label">Off heap bytes</td><td class="value">{{fuint64 .OtherSys}}</td></tr>
</table>
<h2>By size</h2>
<table>
<tr><th>Size</th><th>Mallocs</th><th>Frees</th></tr>
{{range .BySize}}<tr><td class="value">{{fuint32 .Size}}</td><td class="value">{{fuint64 .Mallocs}}</td><td class="value">{{fuint64 .Frees}}</td></tr>{{end}}
</table>
{{end}}
`))

var DebugTmpl = template.Must(template.Must(BaseTemplate.Clone()).Parse(`
{{define "title"}}GoPls Debug pages{{end}}
{{define "body"}}
<a href="/debug/pprof">Profiling</a>
{{end}}
`))

var CacheTmpl = template.Must(template.Must(BaseTemplate.Clone()).Parse(`
{{define "title"}}Cache {{.ID}}{{end}}
{{define "body"}}
<h2>memoize.Store entries</h2>
<ul>{{range $k,$v := .MemStats}}<li>{{$k}} - {{$v}}</li>{{end}}</ul>
<h2>File stats</h2>
<p>
{{- $stats := .FileStats -}}
Total: <b>{{$stats.Total}}</b><br>
Largest: <b>{{$stats.Largest}}</b><br>
Errors: <b>{{$stats.Errs}}</b><br>
</p>
{{end}}
`))

var AnalysisTmpl = template.Must(template.Must(BaseTemplate.Clone()).Parse(`
{{define "title"}}Analysis{{end}}
{{define "body"}}
<h2>Analyzer.Run times</h2>
<ul>{{range .AnalyzerRunTimes}}<li>{{.Duration}} {{.Label}}</li>{{end}}</ul>
{{end}}
`))

var ClientTmpl = template.Must(template.Must(BaseTemplate.Clone()).Parse(`
{{define "title"}}Client {{.Session.ID}}{{end}}
{{define "body"}}
Using session: <b>{{template "sessionlink" .Session.ID}}</b><br>
{{if .DebugAddress}}Debug this client at: <a href="http://{{localAddress .DebugAddress}}">{{localAddress .DebugAddress}}</a><br>{{end}}
Logfile: {{.Logfile}}<br>
Gopls Path: {{.GoplsPath}}<br>
{{end}}
`))

var ServerTmpl = template.Must(template.Must(BaseTemplate.Clone()).Parse(`
{{define "title"}}Server {{.ID}}{{end}}
{{define "body"}}
{{if .DebugAddress}}Debug this server at: <a href="http://{{localAddress .DebugAddress}}">{{localAddress .DebugAddress}}</a><br>{{end}}
Logfile: {{.Logfile}}<br>
Gopls Path: {{.GoplsPath}}<br>
{{end}}
`))

var SessionTmpl = template.Must(template.Must(BaseTemplate.Clone()).Parse(`
{{define "title"}}Session {{.ID}}{{end}}
{{define "body"}}
From: <b>{{template "cachelink" .Cache.ID}}</b><br>
<h2>Views</h2>
<ul>{{range .Views}}
{{- $envOverlay := .EnvOverlay -}}
<li>ID: <b>{{.ID}}</b><br>
Type: <b>{{.Type}}</b><br>
Root: <b>{{.Root}}</b><br>
{{- if $envOverlay}}
Env overlay: <b>{{$envOverlay}})</b><br>
{{end -}}
Folder: <b>{{.Folder.Name}}:{{.Folder.Dir}}</b></li>
{{end}}</ul>
<h2>Overlays</h2>
{{$session := .}}
<ul>{{range .Overlays}}
<li>
<a href="/file/{{$session.ID}}/{{.Identity.Hash}}">{{.Identity.URI}}</a>
</li>{{end}}</ul>
{{end}}
`))

var FileTmpl = template.Must(template.Must(BaseTemplate.Clone()).Parse(`
{{define "title"}}Overlay {{.Identity.Hash}}{{end}}
{{define "body"}}
{{with .}}
	URI: <b>{{.URI}}</b><br>
	Identifier: <b>{{.Identity.Hash}}</b><br>
	Version: <b>{{.Version}}</b><br>
	Kind: <b>{{.Kind}}</b><br>
{{end}}
<h3>Contents</h3>
<pre>{{fcontent .Content}}</pre>
{{end}}
`))
