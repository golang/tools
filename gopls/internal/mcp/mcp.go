// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package mcp

import (
	"context"
	_ "embed"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"sync"

	"golang.org/x/tools/gopls/internal/cache"
	"golang.org/x/tools/gopls/internal/cache/metadata"
	"golang.org/x/tools/gopls/internal/file"
	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/gopls/internal/settings"
	"golang.org/x/tools/gopls/internal/util/moremaps"
	"golang.org/x/tools/internal/mcp"
)

//go:embed instructions.md
var Instructions string

// A handler implements various MCP tools for an LSP session.
type handler struct {
	session   *cache.Session
	lspServer protocol.Server
}

// Sessions is the interface used to access gopls sessions.
type Sessions interface {
	Session(id string) (*cache.Session, protocol.Server)
	FirstSession() (*cache.Session, protocol.Server)
	SetSessionExitFunc(func(string))
}

// Serve starts an MCP server serving at the input address.
// The server receives LSP session events on the specified channel, which the
// caller is responsible for closing. The server runs until the context is
// canceled.
func Serve(ctx context.Context, address string, sessions Sessions, isDaemon bool) error {
	log.Printf("Gopls MCP server: starting up on http")
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return err
	}
	defer listener.Close()

	// TODO(hxjiang): expose the MCP server address to the LSP client.
	if isDaemon {
		log.Printf("Gopls MCP daemon: listening on address %s...", listener.Addr())
	}
	defer log.Printf("Gopls MCP server: exiting")

	svr := http.Server{
		Handler: HTTPHandler(sessions, isDaemon),
		BaseContext: func(net.Listener) context.Context {
			return ctx
		},
	}

	// Run the server until cancellation.
	go func() {
		<-ctx.Done()
		svr.Close() // ignore error
	}()
	log.Printf("mcp http server listening")
	return svr.Serve(listener)
}

// StartStdIO starts an MCP server over stdio.
func StartStdIO(ctx context.Context, session *cache.Session, server protocol.Server, rpcLog io.Writer) error {
	transport := mcp.NewStdioTransport()
	var t mcp.Transport = transport
	if rpcLog != nil {
		t = mcp.NewLoggingTransport(transport, rpcLog)
	}
	s := newServer(session, server)
	return s.Run(ctx, t)
}

func HTTPHandler(sessions Sessions, isDaemon bool) http.Handler {
	var (
		mu          sync.Mutex                         // lock for mcpHandlers.
		mcpHandlers = make(map[string]*mcp.SSEHandler) // map from lsp session ids to MCP sse handlers.
	)
	mux := http.NewServeMux()

	// In daemon mode, gopls serves mcp server at ADDRESS/sessions/$SESSIONID.
	// Otherwise, gopls serves mcp server at ADDRESS.
	if isDaemon {
		mux.HandleFunc("/sessions/{id}", func(w http.ResponseWriter, r *http.Request) {
			sessionID := r.PathValue("id")

			mu.Lock()
			handler, ok := mcpHandlers[sessionID]
			if !ok {
				if s, svr := sessions.Session(sessionID); s != nil {
					handler = mcp.NewSSEHandler(func(request *http.Request) *mcp.Server {
						return newServer(s, svr)
					})
					mcpHandlers[sessionID] = handler
				}
			}
			mu.Unlock()

			if handler == nil {
				http.Error(w, fmt.Sprintf("session %s not established", sessionID), http.StatusNotFound)
				return
			}

			handler.ServeHTTP(w, r)
		})
	} else {
		// TODO(hxjiang): should gopls serve only at a specific path?
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			// When not in daemon mode, gopls has at most one LSP session.
			_, handler, ok := moremaps.Arbitrary(mcpHandlers)
			if !ok {
				s, svr := sessions.FirstSession()
				handler = mcp.NewSSEHandler(func(request *http.Request) *mcp.Server {
					return newServer(s, svr)
				})
				mcpHandlers[s.ID()] = handler
			}
			mu.Unlock()

			if handler == nil {
				http.Error(w, "session not established", http.StatusNotFound)
				return
			}

			handler.ServeHTTP(w, r)
		})
	}
	sessions.SetSessionExitFunc(func(sessionID string) {
		mu.Lock()
		defer mu.Unlock()
		// TODO(rfindley): add a way to close SSE handlers (and therefore
		// close their transports). Otherwise, we leak JSON-RPC goroutines.
		delete(mcpHandlers, sessionID)
	})
	return mux
}

func newServer(session *cache.Session, lspServer protocol.Server) *mcp.Server {
	h := handler{
		session:   session,
		lspServer: lspServer,
	}
	mcpServer := mcp.NewServer("gopls", "v0.1.0", &mcp.ServerOptions{
		Instructions: Instructions,
	})

	defaultTools := []*mcp.ServerTool{
		h.workspaceTool(),
		h.outlineTool(),
		h.workspaceDiagnosticsTool(),
		h.symbolReferencesTool(),
		h.searchTool(),
		h.fileContextTool(),
	}
	disabledTools := append(defaultTools,
		// The fileMetadata tool is redundant with fileContext.
		h.fileMetadataTool(),
		// The context tool returns context for all imports, which can consume a
		// lot of tokens. Conservatively, rely on the model selecting the imports
		// to summarize using the outline tool.
		h.contextTool(),
		// The fileDiagnosticsTool only returns diagnostics for the current file,
		// but often changes will cause breakages in other tools. The
		// workspaceDiagnosticsTool always returns breakages, and supports running
		// deeper diagnostics in selected files.
		h.fileDiagnosticsTool(),
		// The references tool requires a location, which models tend to get wrong.
		// The symbolic variant seems to be easier to get right, albeit less
		// powerful.
		h.referencesTool(),
	)
	var toolConfig map[string]bool // non-default settings
	// For testing, poke through to the gopls server to access its options,
	// and enable some of the disabled tools.
	if hasOpts, ok := lspServer.(interface{ Options() *settings.Options }); ok {
		toolConfig = hasOpts.Options().MCPTools
	}
	var tools []*mcp.ServerTool
	for _, tool := range defaultTools {
		if enabled, ok := toolConfig[tool.Tool.Name]; !ok || enabled {
			tools = append(tools, tool)
		}
	}
	// Disabled tools must be explicitly enabled.
	for _, tool := range disabledTools {
		if toolConfig[tool.Tool.Name] {
			tools = append(tools, tool)
		}
	}
	mcpServer.AddTools(tools...)

	return mcpServer
}

// snapshot returns the best default snapshot to use for workspace queries.
func (h *handler) snapshot() (*cache.Snapshot, func(), error) {
	views := h.session.Views()
	if len(views) == 0 {
		return nil, nil, fmt.Errorf("No active builds.")
	}
	return views[0].Snapshot()
}

// fileOf is like [cache.Session.FileOf], but does a sanity check for file
// changes. Currently, it checks for modified files in the transitive closure
// of the file's narrowest package.
//
// This helps avoid stale packages, but is not a substitute for real file
// watching, as it misses things like files being added to a package.
func (h *handler) fileOf(ctx context.Context, file string) (file.Handle, *cache.Snapshot, func(), error) {
	uri := protocol.URIFromPath(file)
	fh, snapshot, release, err := h.session.FileOf(ctx, uri)
	if err != nil {
		return nil, nil, nil, err
	}
	md, err := snapshot.NarrowestMetadataForFile(ctx, uri)
	if err != nil {
		release()
		return nil, nil, nil, err
	}
	fileEvents, err := checkForFileChanges(ctx, snapshot, md.ID)
	if err != nil {
		release()
		return nil, nil, nil, err
	}
	if len(fileEvents) == 0 {
		return fh, snapshot, release, nil
	}
	release() // snapshot is not latest

	// We detect changed files: process them before getting the snapshot.
	if err := h.lspServer.DidChangeWatchedFiles(ctx, &protocol.DidChangeWatchedFilesParams{
		Changes: fileEvents,
	}); err != nil {
		return nil, nil, nil, err
	}
	return h.session.FileOf(ctx, uri)
}

// checkForFileChanges checks for file changes in the transitive closure of
// the given package, by checking file modification time. Since it does not
// actually read file contents, it may miss changes that occur within the mtime
// resolution of the current file system (on some operating systems, this may
// be as much as a second).
//
// It also doesn't catch package changes that occur due to added files or
// changes to the go.mod file.
func checkForFileChanges(ctx context.Context, snapshot *cache.Snapshot, id metadata.PackageID) ([]protocol.FileEvent, error) {
	var events []protocol.FileEvent

	seen := make(map[metadata.PackageID]struct{})
	var checkPkg func(id metadata.PackageID) error
	checkPkg = func(id metadata.PackageID) error {
		if _, ok := seen[id]; ok {
			return nil
		}
		seen[id] = struct{}{}

		mp := snapshot.Metadata(id)
		for _, uri := range mp.CompiledGoFiles {
			fh, err := snapshot.ReadFile(ctx, uri)
			if err != nil {
				return err // context cancelled
			}

			mtime, mtimeErr := fh.ModTime()
			fi, err := os.Stat(uri.Path())
			switch {
			case err != nil:
				if mtimeErr == nil {
					// file existed, and doesn't anymore, so the file was deleted
					events = append(events, protocol.FileEvent{URI: uri, Type: protocol.Deleted})
				}
			case mtimeErr != nil:
				// err == nil (from above), so the file was created
				events = append(events, protocol.FileEvent{URI: uri, Type: protocol.Created})
			case !mtime.IsZero() && fi.ModTime().After(mtime):
				events = append(events, protocol.FileEvent{URI: uri, Type: protocol.Changed})
			}
		}
		for _, depID := range mp.DepsByPkgPath {
			if err := checkPkg(depID); err != nil {
				return err
			}
		}
		return nil
	}
	return events, checkPkg(id)
}

func textResult(text string) *mcp.CallToolResultFor[any] {
	return &mcp.CallToolResultFor[any]{
		Content: []*mcp.Content{
			mcp.NewTextContent(text),
		},
	}
}
