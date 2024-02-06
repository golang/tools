// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// The file package defines types used for working with LSP files.
package file

import (
	"context"
	"fmt"

	"golang.org/x/tools/gopls/internal/protocol"
)

// An Identity identifies the name and contents of a file.
//
// TODO(rfindley): Identity may not carry its weight. Consider instead just
// exposing Handle.Hash, and using an ad-hoc key type where necessary.
// Or perhaps if mod/work parsing is moved outside of the memoize cache,
// a notion of Identity simply isn't needed.
type Identity struct {
	URI  protocol.DocumentURI
	Hash Hash // digest of file contents
}

func (id Identity) String() string {
	return fmt.Sprintf("%s%s", id.URI, id.Hash)
}

// A FileHandle represents the URI, content, hash, and optional
// version of a file tracked by the LSP session.
//
// File content may be provided by the file system (for Saved files)
// or from an overlay, for open files with unsaved edits.
// A FileHandle may record an attempt to read a non-existent file,
// in which case Content returns an error.
type Handle interface {
	// URI is the URI for this file handle.
	URI() protocol.DocumentURI
	// Identity returns an Identity for the file, even if there was an error
	// reading it.
	Identity() Identity
	// SameContentsOnDisk reports whether the file has the same content on disk:
	// it is false for files open on an editor with unsaved edits.
	SameContentsOnDisk() bool
	// Version returns the file version, as defined by the LSP client.
	// For on-disk file handles, Version returns 0.
	Version() int32
	// Content returns the contents of a file.
	// If the file is not available, returns a nil slice and an error.
	Content() ([]byte, error)
}

// A Source maps URIs to Handles.
type Source interface {
	// ReadFile returns the Handle for a given URI, either by reading the content
	// of the file or by obtaining it from a cache.
	//
	// Invariant: ReadFile must only return an error in the case of context
	// cancellation. If ctx.Err() is nil, the resulting error must also be nil.
	ReadFile(ctx context.Context, uri protocol.DocumentURI) (Handle, error)
}
