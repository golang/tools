// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package cache

import (
	"context"
	"sync"

	"golang.org/x/tools/gopls/internal/file"
	"golang.org/x/tools/gopls/internal/lsp/protocol"
)

// An overlayFS is a file.Source that keeps track of overlays on top of a
// delegate FileSource.
type overlayFS struct {
	delegate file.Source

	mu       sync.Mutex
	overlays map[protocol.DocumentURI]*Overlay
}

func newOverlayFS(delegate file.Source) *overlayFS {
	return &overlayFS{
		delegate: delegate,
		overlays: make(map[protocol.DocumentURI]*Overlay),
	}
}

// Overlays returns a new unordered array of overlays.
func (fs *overlayFS) Overlays() []*Overlay {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	overlays := make([]*Overlay, 0, len(fs.overlays))
	for _, overlay := range fs.overlays {
		overlays = append(overlays, overlay)
	}
	return overlays
}

func (fs *overlayFS) ReadFile(ctx context.Context, uri protocol.DocumentURI) (file.Handle, error) {
	fs.mu.Lock()
	overlay, ok := fs.overlays[uri]
	fs.mu.Unlock()
	if ok {
		return overlay, nil
	}
	return fs.delegate.ReadFile(ctx, uri)
}

// An Overlay is a file open in the editor. It may have unsaved edits.
// It implements the file.Handle interface.
type Overlay struct {
	uri     protocol.DocumentURI
	content []byte
	hash    file.Hash
	version int32
	kind    file.Kind

	// saved is true if a file matches the state on disk,
	// and therefore does not need to be part of the overlay sent to go/packages.
	saved bool
}

func (o *Overlay) URI() protocol.DocumentURI { return o.uri }

func (o *Overlay) Identity() file.Identity {
	return file.Identity{
		URI:  o.uri,
		Hash: o.hash,
	}
}

func (o *Overlay) Content() ([]byte, error) { return o.content, nil }
func (o *Overlay) Version() int32           { return o.version }
func (o *Overlay) SameContentsOnDisk() bool { return o.saved }
func (o *Overlay) Kind() file.Kind          { return o.kind }
