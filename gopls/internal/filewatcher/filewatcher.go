// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package filewatcher

import (
	"fmt"
	"log/slog"

	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/gopls/internal/settings"
)

// Watcher monitors file system events.
type Watcher interface {
	// WatchDir adds a directory to the set of watched directories.
	WatchDir(path string) error

	// Close stops the watcher and releases any associated resources.
	Close() error

	// Poke signals the watcher to prioritize a scan, if applicable.
	// This is used to implement adaptive polling.
	Poke()

	// Mode returns the current mode of the file watcher.
	Mode() settings.FileWatcherMode
}

// New creates a new file watcher and starts its event-handling loop. The
// [Watcher.Close] method must be called to clean up resources.
//
// The provided event handler is called sequentially with a batch of file events,
// but the error handler is called concurrently. The watcher blocks until the
// handler returns, so the handlers should be fast and non-blocking.
func New(mode settings.FileWatcherMode, logger *slog.Logger, onEvents func([]protocol.FileEvent), onError func(error)) (Watcher, error) {
	switch mode {
	case settings.FileWatcherPoll:
		return NewPollWatcher(logger, onEvents, onError), nil
	case settings.FileWatcherFSNotify:
		return NewFSNotifyWatcher(logger, onEvents, onError)
	}
	// TODO(hxjiang): support "auto" mode.
	return nil, fmt.Errorf("unknown FileWatcher mode: %q", mode)
}
