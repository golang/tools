// Copyright 2018 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package cmd handles the gopls command line.
// It contains a handler for each of the modes, along with all the flag handling
// and the command line output format.
package cmd

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"text/tabwriter"
	"time"

	"golang.org/x/tools/gopls/internal/cache"
	"golang.org/x/tools/gopls/internal/debug"
	"golang.org/x/tools/gopls/internal/filecache"
	"golang.org/x/tools/gopls/internal/lsprpc"
	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/gopls/internal/protocol/command"
	"golang.org/x/tools/gopls/internal/server"
	"golang.org/x/tools/gopls/internal/settings"
	"golang.org/x/tools/gopls/internal/util/browser"
	bugpkg "golang.org/x/tools/gopls/internal/util/bug"
	"golang.org/x/tools/gopls/internal/util/constraints"
	"golang.org/x/tools/internal/diff"
	"golang.org/x/tools/internal/jsonrpc2"
	"golang.org/x/tools/internal/tool"
)

// Application is the main application as passed to tool.Main
// It handles the main command line parsing and dispatch to the sub commands.
type Application struct {
	// Core application flags

	// Embed the basic profiling flags supported by the tool package
	tool.Profile

	// We include the server configuration directly for now, so the flags work
	// even without the verb.
	// TODO: Remove this when we stop allowing the serve verb by default.
	Serve Serve

	// the options configuring function to invoke when building a server
	options func(*settings.Options)

	// Support for remote LSP server.
	Remote string `flag:"remote" help:"forward all commands to a remote lsp specified by this flag. With no special prefix, this is assumed to be a TCP address. If prefixed by 'unix;', the subsequent address is assumed to be a unix domain socket. If 'auto', or prefixed by 'auto;', the remote address is automatically resolved based on the executing environment."`

	// Verbose enables verbose logging.
	Verbose bool `flag:"v,verbose" help:"verbose output"`

	// VeryVerbose enables a higher level of verbosity in logging output.
	VeryVerbose bool `flag:"vv,veryverbose" help:"very verbose output"`

	// Control ocagent export of telemetry
	OCAgent string `flag:"ocagent" help:"the address of the ocagent (e.g. http://localhost:55678), or off"`

	// PrepareOptions is called to update the options when a new view is built.
	// It is primarily to allow the behavior of gopls to be modified by hooks.
	PrepareOptions func(*settings.Options)

	// editFlags holds flags that control how file edit operations
	// are applied, in particular when the server makes an ApplyEdits
	// downcall to the client. Present only for commands that apply edits.
	editFlags *EditFlags
}

// EditFlags defines flags common to {fix,format,imports,rename}
// that control how edits are applied to the client's files.
//
// The type is exported for flag reflection.
//
// The -write, -diff, and -list flags are orthogonal but any
// of them suppresses the default behavior, which is to print
// the edited file contents.
type EditFlags struct {
	Write    bool `flag:"w,write" help:"write edited content to source files"`
	Preserve bool `flag:"preserve" help:"with -write, make copies of original files"`
	Diff     bool `flag:"d,diff" help:"display diffs instead of edited file content"`
	List     bool `flag:"l,list" help:"display names of edited files"`
}

func (app *Application) verbose() bool {
	return app.Verbose || app.VeryVerbose
}

// New returns a new Application ready to run.
func New() *Application {
	app := &Application{
		OCAgent: "off", //TODO: Remove this line to default the exporter to on

		Serve: Serve{
			RemoteListenTimeout: 1 * time.Minute,
		},
	}
	app.Serve.app = app
	return app
}

// Name implements tool.Application returning the binary name.
func (app *Application) Name() string { return "gopls" }

// Usage implements tool.Application returning empty extra argument usage.
func (app *Application) Usage() string { return "" }

// ShortHelp implements tool.Application returning the main binary help.
func (app *Application) ShortHelp() string {
	return ""
}

// DetailedHelp implements tool.Application returning the main binary help.
// This includes the short help for all the sub commands.
func (app *Application) DetailedHelp(f *flag.FlagSet) {
	w := tabwriter.NewWriter(f.Output(), 0, 0, 2, ' ', 0)
	defer w.Flush()

	fmt.Fprint(w, `
gopls is a Go language server.

It is typically used with an editor to provide language features. When no
command is specified, gopls will default to the 'serve' command. The language
features can also be accessed via the gopls command-line interface.

Usage:
  gopls help [<subject>]

Command:
`)
	fmt.Fprint(w, "\nMain\t\n")
	for _, c := range app.mainCommands() {
		fmt.Fprintf(w, "  %s\t%s\n", c.Name(), c.ShortHelp())
	}
	fmt.Fprint(w, "\t\nFeatures\t\n")
	for _, c := range app.featureCommands() {
		fmt.Fprintf(w, "  %s\t%s\n", c.Name(), c.ShortHelp())
	}
	if app.verbose() {
		fmt.Fprint(w, "\t\nInternal Use Only\t\n")
		for _, c := range app.internalCommands() {
			fmt.Fprintf(w, "  %s\t%s\n", c.Name(), c.ShortHelp())
		}
	}
	fmt.Fprint(w, "\nflags:\n")
	printFlagDefaults(f)
}

// this is a slightly modified version of flag.PrintDefaults to give us control
func printFlagDefaults(s *flag.FlagSet) {
	var flags [][]*flag.Flag
	seen := map[flag.Value]int{}
	s.VisitAll(func(f *flag.Flag) {
		if i, ok := seen[f.Value]; !ok {
			seen[f.Value] = len(flags)
			flags = append(flags, []*flag.Flag{f})
		} else {
			flags[i] = append(flags[i], f)
		}
	})
	for _, entry := range flags {
		sort.SliceStable(entry, func(i, j int) bool {
			return len(entry[i].Name) < len(entry[j].Name)
		})
		var b strings.Builder
		for i, f := range entry {
			switch i {
			case 0:
				b.WriteString("  -")
			default:
				b.WriteString(",-")
			}
			b.WriteString(f.Name)
		}

		f := entry[0]
		name, usage := flag.UnquoteUsage(f)
		if len(name) > 0 {
			b.WriteString("=")
			b.WriteString(name)
		}
		// Boolean flags of one ASCII letter are so common we
		// treat them specially, putting their usage on the same line.
		if b.Len() <= 4 { // space, space, '-', 'x'.
			b.WriteString("\t")
		} else {
			// Four spaces before the tab triggers good alignment
			// for both 4- and 8-space tab stops.
			b.WriteString("\n    \t")
		}
		b.WriteString(strings.ReplaceAll(usage, "\n", "\n    \t"))
		if !isZeroValue(f, f.DefValue) {
			if reflect.TypeOf(f.Value).Elem().Name() == "stringValue" {
				fmt.Fprintf(&b, " (default %q)", f.DefValue)
			} else {
				fmt.Fprintf(&b, " (default %v)", f.DefValue)
			}
		}
		fmt.Fprint(s.Output(), b.String(), "\n")
	}
}

// isZeroValue is copied from the flags package
func isZeroValue(f *flag.Flag, value string) bool {
	// Build a zero value of the flag's Value type, and see if the
	// result of calling its String method equals the value passed in.
	// This works unless the Value type is itself an interface type.
	typ := reflect.TypeOf(f.Value)
	var z reflect.Value
	if typ.Kind() == reflect.Ptr {
		z = reflect.New(typ.Elem())
	} else {
		z = reflect.Zero(typ)
	}
	return value == z.Interface().(flag.Value).String()
}

// Run takes the args after top level flag processing, and invokes the correct
// sub command as specified by the first argument.
// If no arguments are passed it will invoke the server sub command, as a
// temporary measure for compatibility.
func (app *Application) Run(ctx context.Context, args ...string) error {
	// In the category of "things we can do while waiting for the Go command":
	// Pre-initialize the filecache, which takes ~50ms to hash the gopls
	// executable, and immediately runs a gc.
	filecache.Start()

	ctx = debug.WithInstance(ctx, app.OCAgent)
	if len(args) == 0 {
		s := flag.NewFlagSet(app.Name(), flag.ExitOnError)
		return tool.Run(ctx, s, &app.Serve, args)
	}
	command, args := args[0], args[1:]
	for _, c := range app.Commands() {
		if c.Name() == command {
			s := flag.NewFlagSet(app.Name(), flag.ExitOnError)
			return tool.Run(ctx, s, c, args)
		}
	}
	return tool.CommandLineErrorf("Unknown command %v", command)
}

// Commands returns the set of commands supported by the gopls tool on the
// command line.
// The command is specified by the first non flag argument.
func (app *Application) Commands() []tool.Application {
	var commands []tool.Application
	commands = append(commands, app.mainCommands()...)
	commands = append(commands, app.featureCommands()...)
	commands = append(commands, app.internalCommands()...)
	return commands
}

func (app *Application) mainCommands() []tool.Application {
	return []tool.Application{
		&app.Serve,
		&version{app: app},
		&bug{app: app},
		&help{app: app},
		&apiJSON{app: app},
		&licenses{app: app},
	}
}

func (app *Application) internalCommands() []tool.Application {
	return []tool.Application{
		&vulncheck{app: app},
	}
}

func (app *Application) featureCommands() []tool.Application {
	return []tool.Application{
		&callHierarchy{app: app},
		&check{app: app},
		&codelens{app: app},
		&definition{app: app},
		&execute{app: app},
		&foldingRanges{app: app},
		&format{app: app},
		&highlight{app: app},
		&implementation{app: app},
		&imports{app: app},
		newRemote(app, ""),
		newRemote(app, "inspect"),
		&links{app: app},
		&prepareRename{app: app},
		&references{app: app},
		&rename{app: app},
		&semtok{app: app},
		&signature{app: app},
		&stats{app: app},
		&suggestedFix{app: app},
		&symbols{app: app},

		&workspaceSymbol{app: app},
	}
}

var (
	internalMu          sync.Mutex
	internalConnections = make(map[string]*connection)
)

// connect creates and initializes a new in-process gopls session.
//
// If onProgress is set, it is called for each new progress notification.
func (app *Application) connect(ctx context.Context, onProgress func(*protocol.ProgressParams)) (*connection, error) {
	switch {
	case app.Remote == "":
		client := newClient(app, onProgress)
		options := settings.DefaultOptions(app.options)
		server := server.New(cache.NewSession(ctx, cache.New(nil)), client, options)
		conn := newConnection(server, client)
		if err := conn.initialize(protocol.WithClient(ctx, client), app.options); err != nil {
			return nil, err
		}
		return conn, nil

	default:
		return app.connectRemote(ctx, app.Remote)
	}
}

func (app *Application) connectRemote(ctx context.Context, remote string) (*connection, error) {
	conn, err := lsprpc.ConnectToRemote(ctx, remote)
	if err != nil {
		return nil, err
	}
	stream := jsonrpc2.NewHeaderStream(conn)
	cc := jsonrpc2.NewConn(stream)
	server := protocol.ServerDispatcher(cc)
	client := newClient(app, nil)
	connection := newConnection(server, client)
	ctx = protocol.WithClient(ctx, connection.client)
	cc.Go(ctx,
		protocol.Handlers(
			protocol.ClientHandler(client, jsonrpc2.MethodNotFound)))
	return connection, connection.initialize(ctx, app.options)
}

func (c *connection) initialize(ctx context.Context, options func(*settings.Options)) error {
	wd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("finding workdir: %v", err)
	}
	params := &protocol.ParamInitialize{}
	params.RootURI = protocol.URIFromPath(wd)
	params.Capabilities.Workspace.Configuration = true

	// Make sure to respect configured options when sending initialize request.
	opts := settings.DefaultOptions(options)
	// If you add an additional option here, you must update the map key in connect.
	params.Capabilities.TextDocument.Hover = &protocol.HoverClientCapabilities{
		ContentFormat: []protocol.MarkupKind{opts.PreferredContentFormat},
	}
	params.Capabilities.TextDocument.DocumentSymbol.HierarchicalDocumentSymbolSupport = opts.HierarchicalDocumentSymbolSupport
	params.Capabilities.TextDocument.SemanticTokens = protocol.SemanticTokensClientCapabilities{}
	params.Capabilities.TextDocument.SemanticTokens.Formats = []protocol.TokenFormat{"relative"}
	params.Capabilities.TextDocument.SemanticTokens.Requests.Range = &protocol.Or_ClientSemanticTokensRequestOptions_range{Value: true}
	//params.Capabilities.TextDocument.SemanticTokens.Requests.Range.Value = true
	params.Capabilities.TextDocument.SemanticTokens.Requests.Full = &protocol.Or_ClientSemanticTokensRequestOptions_full{Value: true}
	params.Capabilities.TextDocument.SemanticTokens.TokenTypes = protocol.SemanticTypes()
	params.Capabilities.TextDocument.SemanticTokens.TokenModifiers = protocol.SemanticModifiers()

	// If the subcommand has registered a progress handler, report the progress
	// capability.
	if c.client.onProgress != nil {
		params.Capabilities.Window.WorkDoneProgress = true
	}

	params.InitializationOptions = map[string]interface{}{
		"symbolMatcher": string(opts.SymbolMatcher),
	}
	if _, err := c.Server.Initialize(ctx, params); err != nil {
		return err
	}
	if err := c.Server.Initialized(ctx, &protocol.InitializedParams{}); err != nil {
		return err
	}
	return nil
}

type connection struct {
	protocol.Server
	client *cmdClient
}

// cmdClient defines the protocol.Client interface behavior of the gopls CLI tool.
type cmdClient struct {
	app        *Application
	onProgress func(*protocol.ProgressParams)

	filesMu sync.Mutex // guards files map
	files   map[protocol.DocumentURI]*cmdFile
}

type cmdFile struct {
	uri           protocol.DocumentURI
	mapper        *protocol.Mapper
	err           error
	diagnosticsMu sync.Mutex
	diagnostics   []protocol.Diagnostic
}

func newClient(app *Application, onProgress func(*protocol.ProgressParams)) *cmdClient {
	return &cmdClient{
		app:        app,
		onProgress: onProgress,
		files:      make(map[protocol.DocumentURI]*cmdFile),
	}
}

func newConnection(server protocol.Server, client *cmdClient) *connection {
	return &connection{
		Server: server,
		client: client,
	}
}

func (c *cmdClient) CodeLensRefresh(context.Context) error { return nil }

func (c *cmdClient) FoldingRangeRefresh(context.Context) error { return nil }

func (c *cmdClient) LogTrace(context.Context, *protocol.LogTraceParams) error { return nil }

func (c *cmdClient) ShowMessage(ctx context.Context, p *protocol.ShowMessageParams) error {
	fmt.Fprintf(os.Stderr, "%s: %s\n", p.Type, p.Message)
	return nil
}

func (c *cmdClient) ShowMessageRequest(ctx context.Context, p *protocol.ShowMessageRequestParams) (*protocol.MessageActionItem, error) {
	return nil, nil
}

func (c *cmdClient) LogMessage(ctx context.Context, p *protocol.LogMessageParams) error {
	// This logic causes server logging to be double-prefixed with a timestamp.
	//     2023/11/08 10:50:21 Error:2023/11/08 10:50:21 <actual message>
	// TODO(adonovan): print just p.Message, plus a newline if needed?
	switch p.Type {
	case protocol.Error:
		log.Print("Error:", p.Message)
	case protocol.Warning:
		log.Print("Warning:", p.Message)
	case protocol.Info:
		if c.app.verbose() {
			log.Print("Info:", p.Message)
		}
	case protocol.Log:
		if c.app.verbose() {
			log.Print("Log:", p.Message)
		}
	default:
		if c.app.verbose() {
			log.Print(p.Message)
		}
	}
	return nil
}

func (c *cmdClient) Event(ctx context.Context, t *interface{}) error { return nil }

func (c *cmdClient) RegisterCapability(ctx context.Context, p *protocol.RegistrationParams) error {
	return nil
}

func (c *cmdClient) UnregisterCapability(ctx context.Context, p *protocol.UnregistrationParams) error {
	return nil
}

func (c *cmdClient) WorkspaceFolders(ctx context.Context) ([]protocol.WorkspaceFolder, error) {
	return nil, nil
}

func (c *cmdClient) Configuration(ctx context.Context, p *protocol.ParamConfiguration) ([]interface{}, error) {
	results := make([]interface{}, len(p.Items))
	for i, item := range p.Items {
		if item.Section != "gopls" {
			continue
		}
		m := map[string]interface{}{
			"analyses": map[string]any{
				"fillreturns":    true,
				"nonewvars":      true,
				"noresultvalues": true,
				"undeclaredname": true,
			},
		}
		if c.app.VeryVerbose {
			m["verboseOutput"] = true
		}
		results[i] = m
	}
	return results, nil
}

func (c *cmdClient) ApplyEdit(ctx context.Context, p *protocol.ApplyWorkspaceEditParams) (*protocol.ApplyWorkspaceEditResult, error) {
	if err := c.applyWorkspaceEdit(&p.Edit); err != nil {
		return &protocol.ApplyWorkspaceEditResult{FailureReason: err.Error()}, nil
	}
	return &protocol.ApplyWorkspaceEditResult{Applied: true}, nil
}

// applyWorkspaceEdit applies a complete WorkspaceEdit to the client's
// files, honoring the preferred edit mode specified by cli.app.editMode.
// (Used by rename and by ApplyEdit downcalls.)
func (cli *cmdClient) applyWorkspaceEdit(edit *protocol.WorkspaceEdit) error {
	var orderedURIs []protocol.DocumentURI
	edits := map[protocol.DocumentURI][]protocol.TextEdit{}
	for _, c := range edit.DocumentChanges {
		if c.TextDocumentEdit != nil {
			uri := c.TextDocumentEdit.TextDocument.URI
			edits[uri] = append(edits[uri], protocol.AsTextEdits(c.TextDocumentEdit.Edits)...)
			orderedURIs = append(orderedURIs, uri)
		}
		if c.RenameFile != nil {
			return fmt.Errorf("client does not support file renaming (%s -> %s)",
				c.RenameFile.OldURI,
				c.RenameFile.NewURI)
		}
	}
	sortSlice(orderedURIs)
	for _, uri := range orderedURIs {
		f := cli.openFile(uri)
		if f.err != nil {
			return f.err
		}
		if err := applyTextEdits(f.mapper, edits[uri], cli.app.editFlags); err != nil {
			return err
		}
	}
	return nil
}

func sortSlice[T constraints.Ordered](slice []T) {
	sort.Slice(slice, func(i, j int) bool { return slice[i] < slice[j] })
}

// applyTextEdits applies a list of edits to the mapper file content,
// using the preferred edit mode. It is a no-op if there are no edits.
func applyTextEdits(mapper *protocol.Mapper, edits []protocol.TextEdit, flags *EditFlags) error {
	if len(edits) == 0 {
		return nil
	}
	newContent, renameEdits, err := protocol.ApplyEdits(mapper, edits)
	if err != nil {
		return err
	}

	filename := mapper.URI.Path()

	if flags.List {
		fmt.Println(filename)
	}

	if flags.Write {
		if flags.Preserve {
			if err := os.Rename(filename, filename+".orig"); err != nil {
				return err
			}
		}
		if err := os.WriteFile(filename, newContent, 0644); err != nil {
			return err
		}
	}

	if flags.Diff {
		unified, err := diff.ToUnified(filename+".orig", filename, string(mapper.Content), renameEdits, diff.DefaultContextLines)
		if err != nil {
			return err
		}
		fmt.Print(unified)
	}

	// No flags: just print edited file content.
	// TODO(adonovan): how is this ever useful with multiple files?
	if !(flags.List || flags.Write || flags.Diff) {
		os.Stdout.Write(newContent)
	}

	return nil
}

func (c *cmdClient) PublishDiagnostics(ctx context.Context, p *protocol.PublishDiagnosticsParams) error {
	// Don't worry about diagnostics without versions.
	if p.Version == 0 {
		return nil
	}

	c.filesMu.Lock()
	file := c.getFile(p.URI)
	c.filesMu.Unlock()

	file.diagnosticsMu.Lock()
	defer file.diagnosticsMu.Unlock()
	file.diagnostics = append(file.diagnostics, p.Diagnostics...)

	// Perform a crude in-place deduplication.
	// TODO(golang/go#60122): replace the gopls.diagnose_files
	// command with support for textDocument/diagnostic,
	// so that we don't need to do this de-duplication.
	type key [6]interface{}
	seen := make(map[key]bool)
	out := file.diagnostics[:0]
	for _, d := range file.diagnostics {
		var codeHref string
		if desc := d.CodeDescription; desc != nil {
			codeHref = desc.Href
		}
		k := key{d.Range, d.Severity, d.Code, codeHref, d.Source, d.Message}
		if !seen[k] {
			seen[k] = true
			out = append(out, d)
		}
	}
	file.diagnostics = out

	return nil
}

func (c *cmdClient) Progress(_ context.Context, params *protocol.ProgressParams) error {
	if c.onProgress != nil {
		c.onProgress(params)
	}
	return nil
}

func (c *cmdClient) ShowDocument(ctx context.Context, params *protocol.ShowDocumentParams) (*protocol.ShowDocumentResult, error) {
	var success bool
	if params.External {
		// Open URI in external browser.
		success = browser.Open(params.URI)
	} else {
		// Open file in editor, optionally taking focus and selecting a range.
		// (cmdClient has no editor. Should it fork+exec $EDITOR?)
		log.Printf("Server requested that client editor open %q (takeFocus=%t, selection=%+v)",
			params.URI, params.TakeFocus, params.Selection)
		success = true
	}
	return &protocol.ShowDocumentResult{Success: success}, nil
}

func (c *cmdClient) WorkDoneProgressCreate(context.Context, *protocol.WorkDoneProgressCreateParams) error {
	return nil
}

func (c *cmdClient) DiagnosticRefresh(context.Context) error {
	return nil
}

func (c *cmdClient) InlayHintRefresh(context.Context) error {
	return nil
}

func (c *cmdClient) SemanticTokensRefresh(context.Context) error {
	return nil
}

func (c *cmdClient) InlineValueRefresh(context.Context) error {
	return nil
}

func (c *cmdClient) getFile(uri protocol.DocumentURI) *cmdFile {
	file, found := c.files[uri]
	if !found || file.err != nil {
		file = &cmdFile{
			uri: uri,
		}
		c.files[uri] = file
	}
	if file.mapper == nil {
		content, err := os.ReadFile(uri.Path())
		if err != nil {
			file.err = fmt.Errorf("getFile: %v: %v", uri, err)
			return file
		}
		file.mapper = protocol.NewMapper(uri, content)
	}
	return file
}

func (c *cmdClient) openFile(uri protocol.DocumentURI) *cmdFile {
	c.filesMu.Lock()
	defer c.filesMu.Unlock()
	return c.getFile(uri)
}

// TODO(adonovan): provide convenience helpers to:
// - map a (URI, protocol.Range) to a MappedRange;
// - parse a command-line argument to a MappedRange.
func (c *connection) openFile(ctx context.Context, uri protocol.DocumentURI) (*cmdFile, error) {
	file := c.client.openFile(uri)
	if file.err != nil {
		return nil, file.err
	}

	p := &protocol.DidOpenTextDocumentParams{
		TextDocument: protocol.TextDocumentItem{
			URI:        uri,
			LanguageID: "go",
			Version:    1,
			Text:       string(file.mapper.Content),
		},
	}
	if err := c.Server.DidOpen(ctx, p); err != nil {
		// TODO(adonovan): is this assignment concurrency safe?
		file.err = fmt.Errorf("%v: %v", uri, err)
		return nil, file.err
	}
	return file, nil
}

func (c *connection) semanticTokens(ctx context.Context, p *protocol.SemanticTokensRangeParams) (*protocol.SemanticTokens, error) {
	// use range to avoid limits on full
	resp, err := c.Server.SemanticTokensRange(ctx, p)
	if err != nil {
		return nil, err
	}
	return resp, nil
}

func (c *connection) diagnoseFiles(ctx context.Context, files []protocol.DocumentURI) error {
	cmd, err := command.NewDiagnoseFilesCommand("Diagnose files", command.DiagnoseFilesArgs{
		Files: files,
	})
	if err != nil {
		return err
	}
	_, err = c.ExecuteCommand(ctx, &protocol.ExecuteCommandParams{
		Command:   cmd.Command,
		Arguments: cmd.Arguments,
	})
	return err
}

func (c *connection) terminate(ctx context.Context) {
	//TODO: do we need to handle errors on these calls?
	c.Shutdown(ctx)
	//TODO: right now calling exit terminates the process, we should rethink that
	//server.Exit(ctx)
}

// Implement io.Closer.
func (c *cmdClient) Close() error {
	return nil
}

// -- conversions to span (UTF-8) domain --

// locationSpan converts a protocol (UTF-16) Location to a (UTF-8) span.
// Precondition: the URIs of Location and Mapper match.
func (f *cmdFile) locationSpan(loc protocol.Location) (span, error) {
	// TODO(adonovan): check that l.URI matches m.URI.
	return f.rangeSpan(loc.Range)
}

// rangeSpan converts a protocol (UTF-16) range to a (UTF-8) span.
// The resulting span has valid Positions and Offsets.
func (f *cmdFile) rangeSpan(r protocol.Range) (span, error) {
	start, end, err := f.mapper.RangeOffsets(r)
	if err != nil {
		return span{}, err
	}
	return f.offsetSpan(start, end)
}

// offsetSpan converts a byte-offset interval to a (UTF-8) span.
// The resulting span contains line, column, and offset information.
func (f *cmdFile) offsetSpan(start, end int) (span, error) {
	if start > end {
		return span{}, fmt.Errorf("start offset (%d) > end (%d)", start, end)
	}
	startPoint, err := offsetPoint(f.mapper, start)
	if err != nil {
		return span{}, fmt.Errorf("start: %v", err)
	}
	endPoint, err := offsetPoint(f.mapper, end)
	if err != nil {
		return span{}, fmt.Errorf("end: %v", err)
	}
	return newSpan(f.mapper.URI, startPoint, endPoint), nil
}

// offsetPoint converts a byte offset to a span (UTF-8) point.
// The resulting point contains line, column, and offset information.
func offsetPoint(m *protocol.Mapper, offset int) (point, error) {
	if !(0 <= offset && offset <= len(m.Content)) {
		return point{}, fmt.Errorf("invalid offset %d (want 0-%d)", offset, len(m.Content))
	}
	line, col8 := m.OffsetLineCol8(offset)
	return newPoint(line, col8, offset), nil
}

// -- conversions from span (UTF-8) domain --

// spanLocation converts a (UTF-8) span to a protocol (UTF-16) range.
// Precondition: the URIs of spanLocation and Mapper match.
func (f *cmdFile) spanLocation(s span) (protocol.Location, error) {
	rng, err := f.spanRange(s)
	if err != nil {
		return protocol.Location{}, err
	}
	return f.mapper.RangeLocation(rng), nil
}

// spanRange converts a (UTF-8) span to a protocol (UTF-16) range.
// Precondition: the URIs of span and Mapper match.
func (f *cmdFile) spanRange(s span) (protocol.Range, error) {
	// Assert that we aren't using the wrong mapper.
	// We check only the base name, and case insensitively,
	// because we can't assume clean paths, no symbolic links,
	// case-sensitive directories. The authoritative answer
	// requires querying the file system, and we don't want
	// to do that.
	if !strings.EqualFold(filepath.Base(string(f.mapper.URI)), filepath.Base(string(s.URI()))) {
		return protocol.Range{}, bugpkg.Errorf("mapper is for file %q instead of %q", f.mapper.URI, s.URI())
	}
	start, err := pointPosition(f.mapper, s.Start())
	if err != nil {
		return protocol.Range{}, fmt.Errorf("start: %w", err)
	}
	end, err := pointPosition(f.mapper, s.End())
	if err != nil {
		return protocol.Range{}, fmt.Errorf("end: %w", err)
	}
	return protocol.Range{Start: start, End: end}, nil
}

// pointPosition converts a valid span (UTF-8) point to a protocol (UTF-16) position.
func pointPosition(m *protocol.Mapper, p point) (protocol.Position, error) {
	if p.HasPosition() {
		return m.LineCol8Position(p.Line(), p.Column())
	}
	if p.HasOffset() {
		return m.OffsetPosition(p.Offset())
	}
	return protocol.Position{}, fmt.Errorf("point has neither offset nor line/column")
}
