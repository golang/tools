// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package cache

import (
	"context"
	"sync"

	"golang.org/x/tools/gopls/internal/lsp/protocol"
	"golang.org/x/tools/gopls/internal/lsp/source"
)

// An overlayFS is a source.FileSource that keeps track of overlays on top of a
// delegate FileSource.
type overlayFS struct {
	delegate source.FileSource

	mu       sync.Mutex
	overlays map[protocol.DocumentURI]*Overlay
}

func newOverlayFS(delegate source.FileSource) *overlayFS {
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

func (fs *overlayFS) ReadFile(ctx context.Context, uri protocol.DocumentURI) (source.FileHandle, error) {
	fs.mu.Lock()
	overlay, ok := fs.overlays[uri]
	fs.mu.Unlock()
	if ok {
		return overlay, nil
	}
	return fs.delegate.ReadFile(ctx, uri)
}

// An Overlay is a file open in the editor. It may have unsaved edits.
// It implements the source.FileHandle interface.
type Overlay struct {
	uri     protocol.DocumentURI
	content []byte
	hash    source.Hash
	version int32
	kind    source.FileKind

	// saved is true if a file matches the state on disk,
	// and therefore does not need to be part of the overlay sent to go/packages.
	saved bool
}

func (o *Overlay) URI() protocol.DocumentURI { return o.uri }

func (o *Overlay) FileIdentity() source.FileIdentity {
	return source.FileIdentity{
		URI:  o.uri,
		Hash: o.hash,
	}
}

func (o *Overlay) Content() ([]byte, error) { return o.content, nil }
func (o *Overlay) Version() int32           { return o.version }
func (o *Overlay) SameContentsOnDisk() bool { return o.saved }
func (o *Overlay) Kind() source.FileKind    { return o.kind }
