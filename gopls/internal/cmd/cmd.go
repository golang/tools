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
	"golang.org/x/tools/gopls/internal/protocol/semtok"
	"golang.org/x/tools/gopls/internal/server"
	"golang.org/x/tools/gopls/internal/settings"
	"golang.org/x/tools/gopls/internal/util/browser"
	bugpkg "golang.org/x/tools/gopls/internal/util/bug"
	"golang.org/x/tools/gopls/internal/util/moreslices"
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

	// PrepareOptions is called to update the options when a new view is built.
	// It is primarily to allow the behavior of gopls to be modified by hooks.
	PrepareOptions func(*settings.Options)

	// editFlags holds flags that control how file edit operations
	// are applied, in particular when the server makes an ApplyEdits
	// downcall to the client. Present only for commands that apply edits.
	editFlags *EditFlags
}

// EditFlags defines flags common to {code{action,lens},format,imports,rename}
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

For documentation of all its features, see:

   https://github.com/golang/tools/blob/master/gopls/doc/features

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
	if typ.Kind() == reflect.Pointer {
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

	ctx = debug.WithInstance(ctx)
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
		&check{app: app, Severity: "warning"},
		&codeaction{app: app},
		&codelens{app: app},
		&definition{app: app},
		&execute{app: app},
		&fix{app: app}, // (non-functional)
		&foldingRanges{app: app},
		&format{app: app},
		&headlessMCP{app: app},
		&highlight{app: app},
		&implementation{app: app},
		&imports{app: app},
		newRemote(app, ""),
		newRemote(app, "inspect"),
		&links{app: app},
		&prepareRename{app: app},
		&references{app: app},
		&rename{app: app},
		&semanticToken{app: app},
		&signature{app: app},
		&stats{app: app},
		&symbols{app: app},

		&workspaceSymbol{app: app},
	}
}

// connect creates and initializes a new in-process gopls LSP session.
func (app *Application) connect(ctx context.Context) (*client, *cache.Session, error) {
	root, err := os.Getwd()
	if err != nil {
		return nil, nil, fmt.Errorf("finding workdir: %v", err)
	}
	options := settings.DefaultOptions(app.options)
	client := newClient(app)
	var (
		svr  protocol.Server
		sess *cache.Session
	)
	if app.Remote == "" {
		// local
		sess = cache.NewSession(ctx, cache.New(nil))
		svr = server.New(sess, client, options)
		ctx = protocol.WithClient(ctx, client)
	} else {
		// remote
		netConn, err := lsprpc.ConnectToRemote(ctx, app.Remote)
		if err != nil {
			return nil, nil, err
		}
		stream := jsonrpc2.NewHeaderStream(netConn)
		jsonConn := jsonrpc2.NewConn(stream)
		svr = protocol.ServerDispatcher(jsonConn)
		ctx = protocol.WithClient(ctx, client)
		jsonConn.Go(ctx,
			protocol.Handlers(
				protocol.ClientHandler(client, jsonrpc2.MethodNotFound)))
	}
	if err := client.initialize(ctx, svr, initParams(root, options)); err != nil {
		return nil, nil, err
	}
	return client, sess, nil
}

func initParams(rootDir string, opts *settings.Options) *protocol.ParamInitialize {
	params := &protocol.ParamInitialize{}
	params.RootURI = protocol.URIFromPath(rootDir)
	params.Capabilities.Workspace.Configuration = true

	// If you add an additional option here,
	// you must update the map key of settings.DefaultOptions called in (*Application).connect.
	params.Capabilities.TextDocument.Hover = &protocol.HoverClientCapabilities{
		ContentFormat: []protocol.MarkupKind{opts.PreferredContentFormat},
	}
	params.Capabilities.TextDocument.DocumentSymbol.HierarchicalDocumentSymbolSupport = opts.HierarchicalDocumentSymbolSupport
	params.Capabilities.TextDocument.SemanticTokens = protocol.SemanticTokensClientCapabilities{}
	params.Capabilities.TextDocument.SemanticTokens.Formats = []protocol.TokenFormat{"relative"}
	params.Capabilities.TextDocument.SemanticTokens.Requests.Range = &protocol.Or_ClientSemanticTokensRequestOptions_range{Value: true}
	// params.Capabilities.TextDocument.SemanticTokens.Requests.Range.Value = true
	params.Capabilities.TextDocument.SemanticTokens.Requests.Full = &protocol.Or_ClientSemanticTokensRequestOptions_full{Value: true}
	params.Capabilities.TextDocument.SemanticTokens.TokenTypes = moreslices.ConvertStrings[string](semtok.TokenTypes)
	params.Capabilities.TextDocument.SemanticTokens.TokenModifiers = moreslices.ConvertStrings[string](semtok.TokenModifiers)
	params.Capabilities.TextDocument.CodeAction = protocol.CodeActionClientCapabilities{
		CodeActionLiteralSupport: protocol.ClientCodeActionLiteralOptions{
			CodeActionKind: protocol.ClientCodeActionKindOptions{
				ValueSet: []protocol.CodeActionKind{protocol.Empty}, // => all
			},
		},
	}
	params.Capabilities.Window.WorkDoneProgress = true
	params.Capabilities.Workspace.FileOperations = &protocol.FileOperationClientCapabilities{
		DidCreate: true,
	}
	params.InitializationOptions = map[string]any{
		"symbolMatcher": string(opts.SymbolMatcher),
	}
	return params
}

// initialize performs LSP's two-call client/server handshake.
func (cli *client) initialize(ctx context.Context, server protocol.Server, params *protocol.ParamInitialize) error {
	result, err := server.Initialize(ctx, params)
	if err != nil {
		return err
	}
	if err := server.Initialized(ctx, &protocol.InitializedParams{}); err != nil {
		return err
	}
	cli.server = server
	cli.initializeResult = result
	return nil
}

// client implements [protocol.Client] and defines the LSP client
// operations of the gopls command.
//
// It holds the client-side state of a single client/server
// connection; it conceptually corresponds to a single call to
// connect(2).
type client struct {
	app *Application

	server           protocol.Server
	initializeResult *protocol.InitializeResult // includes server capabilities

	progressMu sync.Mutex
	iwlToken   protocol.ProgressToken
	iwlDone    chan struct{}

	filesMu sync.Mutex // guards files map
	files   map[protocol.DocumentURI]*cmdFile
}

// cmdFile represents an open file in the gopls command LSP client.
type cmdFile struct {
	uri           protocol.DocumentURI
	mapper        *protocol.Mapper
	err           error
	diagnosticsMu sync.Mutex
	diagnostics   []protocol.Diagnostic
}

func newClient(app *Application) *client {
	return &client{
		app:     app,
		files:   make(map[protocol.DocumentURI]*cmdFile),
		iwlDone: make(chan struct{}),
	}
}

func (cli *client) TextDocumentContentRefresh(context.Context, *protocol.TextDocumentContentRefreshParams) error {
	return nil
}

func (cli *client) CodeLensRefresh(context.Context) error { return nil }

func (cli *client) FoldingRangeRefresh(context.Context) error { return nil }

func (cli *client) LogTrace(context.Context, *protocol.LogTraceParams) error { return nil }

func (cli *client) ShowMessage(ctx context.Context, p *protocol.ShowMessageParams) error {
	fmt.Fprintf(os.Stderr, "%s: %s\n", p.Type, p.Message)
	return nil
}

func (cli *client) ShowMessageRequest(ctx context.Context, p *protocol.ShowMessageRequestParams) (*protocol.MessageActionItem, error) {
	return nil, nil
}

func (cli *client) LogMessage(ctx context.Context, p *protocol.LogMessageParams) error {
	// This logic causes server logging to be double-prefixed with a timestamp.
	//     2023/11/08 10:50:21 Error:2023/11/08 10:50:21 <actual message>
	// TODO(adonovan): print just p.Message, plus a newline if needed?
	switch p.Type {
	case protocol.Error:
		log.Print("Error:", p.Message)
	case protocol.Warning:
		log.Print("Warning:", p.Message)
	case protocol.Info:
		if cli.app.verbose() {
			log.Print("Info:", p.Message)
		}
	case protocol.Log:
		if cli.app.verbose() {
			log.Print("Log:", p.Message)
		}
	default:
		if cli.app.verbose() {
			log.Print(p.Message)
		}
	}
	return nil
}

func (cli *client) Event(ctx context.Context, t *any) error { return nil }

func (cli *client) RegisterCapability(ctx context.Context, p *protocol.RegistrationParams) error {
	return nil
}

func (cli *client) UnregisterCapability(ctx context.Context, p *protocol.UnregistrationParams) error {
	return nil
}

func (cli *client) WorkspaceFolders(ctx context.Context) ([]protocol.WorkspaceFolder, error) {
	return nil, nil
}

func (cli *client) Configuration(ctx context.Context, p *protocol.ParamConfiguration) ([]any, error) {
	results := make([]any, len(p.Items))
	for i, item := range p.Items {
		if item.Section != "gopls" {
			continue
		}
		m := map[string]any{
			"analyses": map[string]any{
				"fillreturns":    true,
				"nonewvars":      true,
				"noresultvalues": true,
				"undeclaredname": true,
			},
		}
		if cli.app.VeryVerbose {
			m["verboseOutput"] = true
		}
		results[i] = m
	}
	return results, nil
}

func (cli *client) ApplyEdit(ctx context.Context, p *protocol.ApplyWorkspaceEditParams) (*protocol.ApplyWorkspaceEditResult, error) {
	if err := cli.applyWorkspaceEdit(&p.Edit); err != nil {
		return &protocol.ApplyWorkspaceEditResult{FailureReason: err.Error()}, nil
	}
	return &protocol.ApplyWorkspaceEditResult{Applied: true}, nil
}

// applyWorkspaceEdit applies a complete WorkspaceEdit to the client's
// files, honoring the preferred edit mode specified by cli.app.editMode.
// (Used by rename and by ApplyEdit downcalls.)
//
// See also:
//   - changedFiles in ../test/marker/marker_test.go for the golden-file capturing variant
//   - applyWorkspaceEdit in ../test/integration/fake/editor.go for the Editor variant
func (cli *client) applyWorkspaceEdit(wsedit *protocol.WorkspaceEdit) error {

	create := func(uri protocol.DocumentURI, content []byte) error {
		edits := []diff.Edit{{Start: 0, End: 0, New: string(content)}}
		return updateFile(uri.Path(), nil, content, edits, cli.app.editFlags)
	}

	delete := func(uri protocol.DocumentURI, content []byte) error {
		edits := []diff.Edit{{Start: 0, End: len(content), New: ""}}
		return updateFile(uri.Path(), content, nil, edits, cli.app.editFlags)
	}

	for _, c := range wsedit.DocumentChanges {
		switch {
		case c.TextDocumentEdit != nil:
			f := cli.getFile(c.TextDocumentEdit.TextDocument.URI)
			if f.err != nil {
				return f.err
			}
			// TODO(adonovan): sanity-check c.TextDocumentEdit.TextDocument.Version
			edits := protocol.AsTextEdits(c.TextDocumentEdit.Edits)
			if err := applyTextEdits(f.mapper, edits, cli.app.editFlags); err != nil {
				return err
			}

		case c.CreateFile != nil:
			if err := create(c.CreateFile.URI, []byte{}); err != nil {
				return err
			}

		case c.RenameFile != nil:
			// Analyze as creation + deletion. (NB: loses file mode.)
			f := cli.getFile(c.RenameFile.OldURI)
			if f.err != nil {
				return f.err
			}
			if err := create(c.RenameFile.NewURI, f.mapper.Content); err != nil {
				return err
			}
			if err := delete(f.mapper.URI, f.mapper.Content); err != nil {
				return err
			}

		case c.DeleteFile != nil:
			f := cli.getFile(c.DeleteFile.URI)
			if f.err != nil {
				return f.err
			}
			if err := delete(f.mapper.URI, f.mapper.Content); err != nil {
				return err
			}

		default:
			return fmt.Errorf("unknown DocumentChange: %#v", c)
		}
	}
	return nil
}

// applyTextEdits applies a list of edits to the mapper file content,
// using the preferred edit mode. It is a no-op if there are no edits.
func applyTextEdits(mapper *protocol.Mapper, edits []protocol.TextEdit, flags *EditFlags) error {
	if len(edits) == 0 {
		return nil
	}
	newContent, diffEdits, err := protocol.ApplyEdits(mapper, edits)
	if err != nil {
		return err
	}
	return updateFile(mapper.URI.Path(), mapper.Content, newContent, diffEdits, flags)
}

// updateFile performs a content update operation on the specified file.
// If the old content is nil, the operation creates the file.
// If the new content is nil, the operation deletes the file.
// The flags control whether the operation is written, or merely listed, diffed, or printed.
func updateFile(filename string, old, new []byte, edits []diff.Edit, flags *EditFlags) error {
	if flags.List {
		fmt.Println(filename)
	}

	if flags.Write {
		if flags.Preserve && old != nil { // edit or delete
			if err := os.WriteFile(filename+".orig", old, 0666); err != nil {
				return err
			}
		}

		if new != nil {
			// create or edit
			if err := os.WriteFile(filename, new, 0666); err != nil {
				return err
			}
		} else {
			// delete
			if err := os.Remove(filename); err != nil {
				return err
			}
		}
	}

	if flags.Diff {
		// For diffing, creations and deletions are equivalent
		// updating an empty file and making an existing file empty.
		unified, err := diff.ToUnified(filename+".orig", filename, string(old), edits, diff.DefaultContextLines)
		if err != nil {
			return err
		}
		fmt.Print(unified)
	}

	// No flags: just print edited file content.
	//
	// This makes no sense for multiple files.
	// (We should probably change the default to -diff.)
	if !(flags.List || flags.Write || flags.Diff) {
		os.Stdout.Write(new)
	}

	return nil
}

func (cli *client) PublishDiagnostics(ctx context.Context, p *protocol.PublishDiagnosticsParams) error {
	// Don't worry about diagnostics without versions.
	//
	// (Note: the representation of PublishDiagnosticsParams
	// cannot distinguish a missing Version from v0, but the
	// server never sends back an explicit zero.)
	if p.Version == 0 {
		return nil
	}

	file := cli.getFile(p.URI)

	file.diagnosticsMu.Lock()
	defer file.diagnosticsMu.Unlock()

	file.diagnostics = append(file.diagnostics, p.Diagnostics...)

	// Perform a crude in-place deduplication.
	// TODO(golang/go#60122): replace the gopls.diagnose_files
	// command with support for textDocument/diagnostic,
	// so that we don't need to do this de-duplication.
	type key [6]any
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

func (cli *client) Progress(_ context.Context, params *protocol.ProgressParams) error {
	if _, ok := params.Token.(string); !ok {
		return fmt.Errorf("unexpected progress token: %[1]T %[1]v", params.Token)
	}

	switch v := params.Value.(type) {
	case *protocol.WorkDoneProgressBegin:
		if v.Title == server.DiagnosticWorkTitle(server.FromInitialWorkspaceLoad) {
			cli.progressMu.Lock()
			cli.iwlToken = params.Token
			cli.progressMu.Unlock()
		}

	case *protocol.WorkDoneProgressReport:
		if cli.app.Verbose {
			fmt.Fprintln(os.Stderr, v.Message)
		}

	case *protocol.WorkDoneProgressEnd:
		cli.progressMu.Lock()
		iwlToken := cli.iwlToken
		cli.progressMu.Unlock()

		if params.Token == iwlToken {
			close(cli.iwlDone)
		}
	}
	return nil
}

func (cli *client) ShowDocument(ctx context.Context, params *protocol.ShowDocumentParams) (*protocol.ShowDocumentResult, error) {
	var success bool
	if params.External {
		// Open URI in external browser.
		success = browser.Open(params.URI)
	} else {
		// Open file in editor, optionally taking focus and selecting a range.
		// (client has no editor. Should it fork+exec $EDITOR?)
		log.Printf("Server requested that client editor open %q (takeFocus=%t, selection=%+v)",
			params.URI, params.TakeFocus, params.Selection)
		success = true
	}
	return &protocol.ShowDocumentResult{Success: success}, nil
}

func (cli *client) WorkDoneProgressCreate(context.Context, *protocol.WorkDoneProgressCreateParams) error {
	return nil
}

func (cli *client) DiagnosticRefresh(context.Context) error {
	return nil
}

func (cli *client) InlayHintRefresh(context.Context) error {
	return nil
}

func (cli *client) SemanticTokensRefresh(context.Context) error {
	return nil
}

func (cli *client) InlineValueRefresh(context.Context) error {
	return nil
}

// getFile returns the specified file, adding it to the client state if needed.
func (cli *client) getFile(uri protocol.DocumentURI) *cmdFile {
	cli.filesMu.Lock()
	defer cli.filesMu.Unlock()

	file, found := cli.files[uri]
	if !found || file.err != nil {
		file = &cmdFile{
			uri: uri,
		}
		cli.files[uri] = file
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

// openFile returns the specified file, adding it to the client state
// if needed, and notifying the server that it was opened.
func (cli *client) openFile(ctx context.Context, uri protocol.DocumentURI) (*cmdFile, error) {
	file := cli.getFile(uri)
	if file.err != nil {
		return nil, file.err
	}

	// Choose language ID from file extension.
	var langID protocol.LanguageKind // "" eventually maps to file.UnknownKind
	switch filepath.Ext(uri.Path()) {
	case ".go":
		langID = "go"
	case ".mod":
		langID = "go.mod"
	case ".sum":
		langID = "go.sum"
	case ".work":
		langID = "go.work"
	case ".s":
		langID = "go.s"
	}

	p := &protocol.DidOpenTextDocumentParams{
		TextDocument: protocol.TextDocumentItem{
			URI:        uri,
			LanguageID: langID,
			Version:    1,
			Text:       string(file.mapper.Content),
		},
	}
	if err := cli.server.DidOpen(ctx, p); err != nil {
		// TODO(adonovan): is this assignment concurrency safe?
		file.err = fmt.Errorf("%v: %v", uri, err)
		return nil, file.err
	}
	return file, nil
}

func diagnoseFiles(ctx context.Context, server protocol.Server, files []protocol.DocumentURI) error {
	cmd := command.NewDiagnoseFilesCommand("Diagnose files", command.DiagnoseFilesArgs{
		Files: files,
	})
	_, err := executeCommand(ctx, server, cmd)
	return err
}

func (cli *client) terminate(ctx context.Context) {
	if err := cli.server.Shutdown(ctx); err != nil {
		log.Printf("server shutdown failed: %v", err)
	}

	// Don't call Exit as it terminates the server process,
	// which is the same as this client process.
	// c.server.Exit(ctx)
}

// Implement io.Closer.
func (cli *client) Close() error {
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
	return f.mapper.URI.Location(rng), nil
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
	if !strings.EqualFold(f.mapper.URI.Base(), s.URI().Base()) {
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

// TODO(adonovan): delete in 2025.
type fix struct{ app *Application }

func (*fix) Name() string       { return "fix" }
func (cmd *fix) Parent() string { return cmd.app.Name() }
func (*fix) Usage() string      { return "" }
func (*fix) ShortHelp() string  { return "apply suggested fixes (obsolete)" }
func (*fix) DetailedHelp(flags *flag.FlagSet) {
	fmt.Fprintf(flags.Output(), `No longer supported; use "gopls codeaction" instead.`)
}
func (*fix) Run(ctx context.Context, args ...string) error {
	return tool.CommandLineErrorf(`no longer supported; use "gopls codeaction" instead`)
}
