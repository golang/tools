// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package filewatcher

import (
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"golang.org/x/tools/gopls/internal/protocol"
)

// Watcher collects events from a [fsnotify.Watcher] and converts them into
// batched LSP [protocol.FileEvent]s.
type Watcher struct {
	logger *slog.Logger

	stop chan struct{} // closed by Close to terminate run loop

	wg sync.WaitGroup // counts number of active run goroutines (max 1)

	watcher *fsnotify.Watcher

	mu sync.Mutex // guards all fields below

	// knownDirs tracks all known directories to help distinguish between file
	// and directory deletion events.
	knownDirs map[string]bool

	// events is the current batch of unsent file events, which will be sent
	// when the timer expires.
	events []protocol.FileEvent
}

// New creates a new file watcher and starts its event-handling loop. The
// [Watcher.Close] method must be called to clean up resources.
//
// The provided handler is called sequentially with either a batch of file
// events or an error. Events and errors may be interleaved. The watcher blocks
// until the handler returns, so the handler should be fast and non-blocking.
func New(delay time.Duration, logger *slog.Logger, handler func([]protocol.FileEvent, error)) (*Watcher, error) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	w := &Watcher{
		logger:    logger,
		watcher:   watcher,
		knownDirs: make(map[string]bool),
		stop:      make(chan struct{}),
	}

	w.wg.Add(1)
	go w.run(delay, handler)

	return w, nil
}

// run is the main event-handling loop for the watcher. It should be run in a
// separate goroutine.
func (w *Watcher) run(delay time.Duration, handler func([]protocol.FileEvent, error)) {
	defer w.wg.Done()

	// timer is used to debounce events.
	timer := time.NewTimer(delay)
	defer timer.Stop()

	for {
		select {
		case <-w.stop:
			return

		case <-timer.C:
			w.sendEvents(handler)
			timer.Reset(delay)

		case err, ok := <-w.watcher.Errors:
			// When the watcher is closed, its Errors channel is closed, which
			// unblocks this case. We continue to the next loop iteration,
			// allowing the <-w.closed case to handle the shutdown.
			if !ok {
				continue
			}
			if err != nil {
				handler(nil, err)
			}

		case event, ok := <-w.watcher.Events:
			if !ok {
				continue
			}
			// file watcher should not handle the fsnotify.Event concurrently,
			// the original order should be preserved. E.g. if a file get
			// deleted and recreated, running concurrently may result it in
			// reverse order.
			//
			// Only reset the timer if a relevant event happened.
			// https://github.com/fsnotify/fsnotify?tab=readme-ov-file#why-do-i-get-many-chmod-events
			if e := w.handleEvent(event); e != nil {
				w.addEvent(*e)
				timer.Reset(delay)
			}
		}
	}
}

// skipDir reports whether the input dir should be skipped.
// Directories that are unlikely to contain Go source files relevant for
// analysis, such as .git directories or testdata, should be skipped to
// avoid unnecessary file system notifications. This reduces noise and
// improves efficiency. Conversely, any directory that might contain Go
// source code should be watched to ensure that gopls can respond to
// file changes.
func skipDir(dirName string) bool {
	// TODO(hxjiang): the file watcher should honor gopls directory
	// filter or the new go.mod ignore directive, or actively listening
	// to gopls register capability request with method
	// "workspace/didChangeWatchedFiles" like a real LSP client.
	return strings.HasPrefix(dirName, ".") || strings.HasPrefix(dirName, "_") || dirName == "testdata"
}

// WatchDir walks through the directory and all its subdirectories, adding
// them to the watcher.
func (w *Watcher) WatchDir(path string) error {
	return filepath.WalkDir(filepath.Clean(path), func(path string, d fs.DirEntry, err error) error {
		if d.IsDir() {
			if skipDir(d.Name()) {
				return filepath.SkipDir
			}
			w.addKnownDir(path)
			if err := w.watchDir(path); err != nil {
				// TODO(hxjiang): retry on watch failures.
				return filepath.SkipDir
			}
		}
		return nil
	})
}

// handleEvent converts an [fsnotify.Event] to the corresponding [protocol.FileEvent]
// and updates the watcher state, returning nil if the event is not relevant.
//
// To avoid blocking, any required watches for new subdirectories are registered
// asynchronously in a separate goroutine.
func (w *Watcher) handleEvent(event fsnotify.Event) *protocol.FileEvent {
	// fsnotify does not guarantee clean filepaths.
	path := filepath.Clean(event.Name)

	var isDir bool
	if info, err := os.Stat(path); err == nil {
		isDir = info.IsDir()
	} else if os.IsNotExist(err) {
		// Upon deletion, the file/dir has been removed. fsnotify
		// does not provide information regarding the deleted item.
		// Use the watchedDirs to determine whether it's a dir.
		isDir = w.isKnownDir(path)
	} else {
		// If statting failed, something is wrong with the file system.
		// Log and move on.
		if w.logger != nil {
			w.logger.Error("failed to stat path, skipping event as its type (file/dir) is unknown", "path", path, "err", err)
		}
		return nil
	}

	if isDir {
		if skipDir(filepath.Base(path)) {
			return nil
		}

		switch {
		case event.Op.Has(fsnotify.Rename):
			// A rename is treated as a deletion of the old path because the
			// fsnotify RENAME event doesn't include the new path. A separate
			// CREATE event will be sent for the new path if the destination
			// directory is watched.
			fallthrough
		case event.Op.Has(fsnotify.Remove):
			// Upon removal, we only need to remove the entries from the map.
			// The [fsnotify.Watcher] remove the watch for us.
			// fsnotify/fsnotify#268
			w.removeKnownDir(path)

			// TODO(hxjiang): Directory removal events from some LSP clients may
			// not include corresponding removal events for child files and
			// subdirectories. Should we do some filtering when add the dir
			// deletion event to the events slice.
			return &protocol.FileEvent{
				URI:  protocol.URIFromPath(path),
				Type: protocol.Deleted,
			}
		case event.Op.Has(fsnotify.Create):
			w.addKnownDir(path)

			// This watch is added asynchronously to prevent a potential deadlock
			// on Windows. The fsnotify library can block when registering a watch
			// if its event channel is full (see fsnotify/fsnotify#502).
			// TODO(hxjiang): retry on watch failure.
			go w.watchDir(path)

			return &protocol.FileEvent{
				URI:  protocol.URIFromPath(path),
				Type: protocol.Created,
			}
		default:
			return nil
		}
	} else {
		// Only watch files of interest.
		switch strings.TrimPrefix(filepath.Ext(path), ".") {
		case "go", "mod", "sum", "work", "s":
		default:
			return nil
		}

		var t protocol.FileChangeType
		switch {
		case event.Op.Has(fsnotify.Rename):
			// A rename is treated as a deletion of the old path because the
			// fsnotify RENAME event doesn't include the new path. A separate
			// CREATE event will be sent for the new path if the destination
			// directory is watched.
			fallthrough
		case event.Op.Has(fsnotify.Remove):
			t = protocol.Deleted
		case event.Op.Has(fsnotify.Create):
			t = protocol.Created
		case event.Op.Has(fsnotify.Write):
			t = protocol.Changed
		default:
			return nil // ignore the rest of the events
		}
		return &protocol.FileEvent{
			URI:  protocol.URIFromPath(path),
			Type: t,
		}
	}
}

// watchDir register the watch for the input dir. This function may be blocking
// because of the issue fsnotify/fsnotify#502.
func (w *Watcher) watchDir(path string) error {
	// Dir with broken symbolic link can not be watched.
	// TODO(hxjiang): is it possible the files/dirs are
	// created before the watch is successfully registered.
	return w.watcher.Add(path)
}

func (w *Watcher) addKnownDir(path string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.knownDirs[path] = true
}

func (w *Watcher) removeKnownDir(path string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	delete(w.knownDirs, path)
}

func (w *Watcher) isKnownDir(path string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()

	_, isDir := w.knownDirs[path]
	return isDir
}

func (w *Watcher) addEvent(event protocol.FileEvent) {
	w.mu.Lock()
	defer w.mu.Unlock()

	// Some systems emit duplicate change events in close
	// succession upon file modification. While the current
	// deduplication is naive and only handles immediate duplicates,
	// a more robust solution is needed.
	//
	// TODO(hxjiang): Enhance deduplication. The current batching of
	// events means all duplicates, regardless of proximity, should
	// be removed. Consider checking the entire buffered slice or
	// using a map for this.
	if len(w.events) == 0 || w.events[len(w.events)-1] != event {
		w.events = append(w.events, event)
	}
}

func (w *Watcher) sendEvents(handler func([]protocol.FileEvent, error)) {
	w.mu.Lock()
	events := w.events
	w.events = nil
	w.mu.Unlock()

	if len(events) != 0 {
		handler(events, nil)
	}
}

// Close shuts down the watcher, waits for the internal goroutine to terminate,
// and returns any final error.
func (w *Watcher) Close() error {
	err := w.watcher.Close()

	// Wait for the go routine to finish. So all the channels will be closed and
	// all go routine will be terminated.
	close(w.stop)

	w.wg.Wait()

	return err
}
