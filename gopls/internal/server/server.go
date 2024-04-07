// Copyright 2018 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package server defines gopls' implementation of the LSP server
// interface, [protocol.Server]. Call [New] to create an instance.
package server

import (
	"context"
	"crypto/rand"
	"embed"
	"encoding/base64"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	paths "path"
	"strconv"
	"strings"
	"sync"

	"golang.org/x/tools/gopls/internal/cache"
	"golang.org/x/tools/gopls/internal/cache/metadata"
	"golang.org/x/tools/gopls/internal/golang"
	"golang.org/x/tools/gopls/internal/progress"
	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/gopls/internal/settings"
	"golang.org/x/tools/gopls/internal/util/bug"
	"golang.org/x/tools/internal/event"
)

// New creates an LSP server and binds it to handle incoming client
// messages on the supplied stream.
func New(session *cache.Session, client protocol.ClientCloser, options *settings.Options) protocol.Server {
	const concurrentAnalyses = 1
	// If this assignment fails to compile after a protocol
	// upgrade, it means that one or more new methods need new
	// stub declarations in unimplemented.go.
	return &server{
		diagnostics:         make(map[protocol.DocumentURI]*fileDiagnostics),
		watchedGlobPatterns: nil, // empty
		changedFiles:        make(map[protocol.DocumentURI]unit),
		session:             session,
		client:              client,
		diagnosticsSema:     make(chan unit, concurrentAnalyses),
		progress:            progress.NewTracker(client),
		options:             options,
		viewsToDiagnose:     make(map[*cache.View]uint64),
	}
}

type serverState int

const (
	serverCreated      = serverState(iota)
	serverInitializing // set once the server has received "initialize" request
	serverInitialized  // set once the server has received "initialized" request
	serverShutDown
)

func (s serverState) String() string {
	switch s {
	case serverCreated:
		return "created"
	case serverInitializing:
		return "initializing"
	case serverInitialized:
		return "initialized"
	case serverShutDown:
		return "shutDown"
	}
	return fmt.Sprintf("(unknown state: %d)", int(s))
}

// server implements the protocol.server interface.
type server struct {
	client protocol.ClientCloser

	stateMu sync.Mutex
	state   serverState
	// notifications generated before serverInitialized
	notifications []*protocol.ShowMessageParams

	session *cache.Session

	tempDir string

	// changedFiles tracks files for which there has been a textDocument/didChange.
	changedFilesMu sync.Mutex
	changedFiles   map[protocol.DocumentURI]unit

	// folders is only valid between initialize and initialized, and holds the
	// set of folders to build views for when we are ready.
	// Each has a valid, non-empty 'file'-scheme URI.
	pendingFolders []protocol.WorkspaceFolder

	// watchedGlobPatterns is the set of glob patterns that we have requested
	// the client watch on disk. It will be updated as the set of directories
	// that the server should watch changes.
	// The map field may be reassigned but the map is immutable.
	watchedGlobPatternsMu  sync.Mutex
	watchedGlobPatterns    map[protocol.RelativePattern]unit
	watchRegistrationCount int

	diagnosticsMu sync.Mutex
	diagnostics   map[protocol.DocumentURI]*fileDiagnostics

	// diagnosticsSema limits the concurrency of diagnostics runs, which can be
	// expensive.
	diagnosticsSema chan unit

	progress *progress.Tracker

	// When the workspace fails to load, we show its status through a progress
	// report with an error message.
	criticalErrorStatusMu sync.Mutex
	criticalErrorStatus   *progress.WorkDone

	// Track an ongoing CPU profile created with the StartProfile command and
	// terminated with the StopProfile command.
	ongoingProfileMu sync.Mutex
	ongoingProfile   *os.File // if non-nil, an ongoing profile is writing to this file

	// Track most recently requested options.
	optionsMu sync.Mutex
	options   *settings.Options

	// Track the most recent completion results, for measuring completion efficacy
	efficacyMu      sync.Mutex
	efficacyURI     protocol.DocumentURI
	efficacyVersion int32
	efficacyItems   []protocol.CompletionItem
	efficacyPos     protocol.Position

	// Web server (for package documentation, etc) associated with this
	// LSP server. Opened on demand, and closed during LSP Shutdown.
	webOnce sync.Once
	web     *web
	webErr  error

	// # Modification tracking and diagnostics
	//
	// For the purpose of tracking diagnostics, we need a monotonically
	// increasing clock. Each time a change occurs on the server, this clock is
	// incremented and the previous diagnostics pass is cancelled. When the
	// changed is processed, the Session (via DidModifyFiles) determines which
	// Views are affected by the change and these views are added to the
	// viewsToDiagnose set. Then the server calls diagnoseChangedViews
	// in a separate goroutine. Any Views that successfully complete their
	// diagnostics are removed from the viewsToDiagnose set, provided they haven't
	// been subsequently marked for re-diagnosis (as determined by the latest
	// modificationID referenced by viewsToDiagnose).
	//
	// In this way, we enforce eventual completeness of the diagnostic set: any
	// views requiring diagnosis are diagnosed, though possibly at a later point
	// in time. Notably, the logic in Session.DidModifyFiles to determines if a
	// view needs diagnosis considers whether any packages in the view were
	// invalidated. Consider the following sequence of snapshots for a given view
	// V:
	//
	//     C1    C2
	//  S1 -> S2 -> S3
	//
	// In this case, suppose that S1 was fully type checked, and then two changes
	// C1 and C2 occur in rapid succession, to a file in their package graph but
	// perhaps not enclosed by V's root.  In this case, the logic of
	// DidModifyFiles will detect that V needs to be reloaded following C1. In
	// order for our eventual consistency to be sound, we need to avoid the race
	// where S2 is being diagnosed, C2 arrives, and S3 is not detected as needing
	// diagnosis because the relevant package has not yet been computed in S2. To
	// achieve this, we only remove V from viewsToDiagnose if the diagnosis of S2
	// completes before C2 is processed, which we can confirm by checking
	// S2.BackgroundContext().
	modificationMu        sync.Mutex
	cancelPrevDiagnostics func()
	viewsToDiagnose       map[*cache.View]uint64 // View -> modification at which it last required diagnosis
	lastModificationID    uint64                 // incrementing clock
}

func (s *server) WorkDoneProgressCancel(ctx context.Context, params *protocol.WorkDoneProgressCancelParams) error {
	ctx, done := event.Start(ctx, "lsp.Server.workDoneProgressCancel")
	defer done()

	return s.progress.Cancel(params.Token)
}

// web encapsulates the web server associated with an LSP server.
// It is used for package documentation and other queries
// where HTML makes more sense than a client editor UI.
//
// Example URL:
//
//	http://127.0.0.1:PORT/gopls/SECRET/...
//
// where
//   - PORT is the random port number;
//   - "gopls" helps the reader guess which program is the server;
//   - SECRET is the 64-bit token; and
//   - ... is the material part of the endpoint.
//
// Valid endpoints:
//
//	open?file=%s&line=%d&col=%d       - open a file
//	pkg/PKGPATH?view=%s               - show doc for package in a given view
type web struct {
	server *http.Server
	addr   url.URL // "http://127.0.0.1:PORT/gopls/SECRET"
	mux    *http.ServeMux
}

// getWeb returns the web server associated with this
// LSP server, creating it on first request.
func (s *server) getWeb() (*web, error) {
	s.webOnce.Do(func() {
		s.web, s.webErr = s.initWeb()
	})
	return s.web, s.webErr
}

// initWeb starts the local web server through which gopls
// serves package documentation and suchlike.
//
// Clients should use [getWeb].
func (s *server) initWeb() (*web, error) {
	// Use 64 random bits as the base of the URL namespace.
	// This ensures that URLs are unguessable to any local
	// processes that connect to the server, preventing
	// exfiltration of source code.
	//
	// (Note: depending on the LSP client, URLs that are passed to
	// it via showDocument and that result in the opening of a
	// browser tab may be transiently published through the argv
	// array of the open(1) or xdg-open(1) command.)
	token := make([]byte, 8)
	if _, err := rand.Read(token); err != nil {
		return nil, fmt.Errorf("generating secret token: %v", err)
	}

	// Pick any free port.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}

	// -- There should be no early returns after this point. --

	// The root mux is not authenticated.
	rootMux := http.NewServeMux()
	rootMux.HandleFunc("/", func(w http.ResponseWriter, req *http.Request) {
		http.Error(w, "request URI lacks authentication segment", http.StatusUnauthorized)
	})
	rootMux.HandleFunc("/favicon.ico", func(w http.ResponseWriter, req *http.Request) {
		http.Redirect(w, req, "/assets/favicon.ico", http.StatusMovedPermanently)
	})
	rootMux.HandleFunc("/hang", func(w http.ResponseWriter, req *http.Request) {
		// This endpoint hangs until cancelled.
		// It is used by JS to detect server disconnect.
		<-req.Context().Done()
	})
	rootMux.Handle("/assets/", http.FileServer(http.FS(assets)))

	secret := "/gopls/" + base64.RawURLEncoding.EncodeToString(token)
	webMux := http.NewServeMux()
	rootMux.Handle(secret+"/", withPanicHandler(http.StripPrefix(secret, webMux)))

	webServer := &http.Server{Addr: listener.Addr().String(), Handler: rootMux}
	go func() {
		// This should run until LSP Shutdown, at which point
		// it will return ErrServerClosed. Any other error
		// means it failed to start.
		if err := webServer.Serve(listener); err != nil {
			if err != http.ErrServerClosed {
				log.Print(err)
			}
		}
	}()

	web := &web{
		server: webServer,
		addr:   url.URL{Scheme: "http", Host: webServer.Addr, Path: secret},
		mux:    webMux,
	}

	// The /open handler allows the browser to request that the
	// LSP client editor open a file; see web.urlToOpen.
	webMux.HandleFunc("/open", func(w http.ResponseWriter, req *http.Request) {
		if err := req.ParseForm(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		uri := protocol.URIFromPath(req.Form.Get("file"))
		line, _ := strconv.Atoi(req.Form.Get("line")) // 1-based
		col, _ := strconv.Atoi(req.Form.Get("col"))   // 1-based UTF-8
		posn := protocol.Position{
			Line:      uint32(line - 1),
			Character: uint32(col - 1), // TODO(adonovan): map to UTF-16
		}
		openClientEditor(req.Context(), s.client, protocol.Location{
			URI:   uri,
			Range: protocol.Range{Start: posn, End: posn},
		})
	})

	// The /pkg/PATH&view=... handler shows package documentation for PATH.
	webMux.Handle("/pkg/", http.StripPrefix("/pkg/", http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		ctx := req.Context()
		if err := req.ParseForm(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		// Get snapshot of specified view.
		view, err := s.session.View(req.Form.Get("view"))
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		snapshot, release, err := view.Snapshot()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		defer release()

		// Find package by path.
		var found *metadata.Package
		for _, mp := range snapshot.MetadataGraph().Packages {
			if string(mp.PkgPath) == req.URL.Path && mp.ForTest == "" {
				found = mp
				break
			}
		}
		if found == nil {
			// TODO(adonovan): what should we do for external test packages?
			http.Error(w, "package not found", http.StatusNotFound)
			return
		}

		// Type-check the package and render its documentation.
		pkgs, err := snapshot.TypeCheck(ctx, found.ID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		pkgURL := func(path golang.PackagePath, fragment string) protocol.URI {
			return web.pkgURL(view, path, fragment)
		}
		content, err := golang.RenderPackageDoc(pkgs[0], web.openURL, pkgURL)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Write(content)
	})))

	return web, nil
}

// assets holds our static web server content.
//
//go:embed assets/*
var assets embed.FS

// openURL returns an /open URL that, when visited, causes the client
// editor to open the specified file/line/column (in 1-based UTF-8
// coordinates).
//
// (Rendering may generate hundreds of positions across files of many
// packages, so don't convert to LSP coordinates yet: wait until the
// URL is opened.)
func (w *web) openURL(filename string, line, col8 int) protocol.URI {
	return w.url(
		"open",
		fmt.Sprintf("file=%s&line=%d&col=%d", url.QueryEscape(filename), line, col8),
		"")
}

// pkgURL returns a /pkg URL for the documentation of the specified package.
// The optional fragment must be of the form "Println" or "Buffer.WriteString".
func (w *web) pkgURL(v *cache.View, path golang.PackagePath, fragment string) protocol.URI {
	return w.url(
		"pkg/"+string(path),
		"view="+url.QueryEscape(v.ID()),
		fragment)
}

// url returns a URL by joining a relative path, an (encoded) query,
// and an (unencoded) fragment onto the authenticated base URL of the
// web server.
func (w *web) url(path, query, fragment string) protocol.URI {
	url2 := w.addr
	url2.Path = paths.Join(url2.Path, strings.TrimPrefix(path, "/"))
	url2.RawQuery = query
	url2.Fragment = fragment
	return protocol.URI(url2.String())
}

// withPanicHandler wraps an HTTP handler with telemetry-reporting of
// panics that would otherwise be silently recovered by the net/http
// root handler.
func withPanicHandler(h http.Handler) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		panicked := true
		defer func() {
			if panicked {
				bug.Report("panic in HTTP handler")
			}
		}()
		h.ServeHTTP(w, req)
		panicked = false
	}
}
