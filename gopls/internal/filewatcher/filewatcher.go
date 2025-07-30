// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package filewatcher

import (
	"errors"
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

// ErrClosed is used when trying to operate on a closed Watcher.
var ErrClosed = errors.New("file watcher: watcher already closed")

// Watcher collects events from a [fsnotify.Watcher] and converts them into
// batched LSP [protocol.FileEvent]s.
type Watcher struct {
	logger *slog.Logger

	stop chan struct{} // closed by Close to terminate run loop

	// errs is an internal channel for surfacing errors from the file watcher,
	// distinct from the fsnotify watcher's error channel.
	errs chan error

	runners sync.WaitGroup // counts the number of active run goroutines (max 1)

	watcher *fsnotify.Watcher

	mu sync.Mutex // guards all fields below

	// watchers counts the number of active watch registration goroutines,
	// including their error handling.
	// After [Watcher.Close] called, watchers's counter will no longer increase.
	watchers sync.WaitGroup

	// dirCancel maps a directory path to its cancellation channel.
	// A nil map indicates the watcher is closing and prevents new directory
	// watch registrations.
	dirCancel map[string]chan struct{}

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
		dirCancel: make(map[string]chan struct{}),
		errs:      make(chan error),
		stop:      make(chan struct{}),
	}

	w.runners.Add(1)
	go w.run(delay, handler)

	return w, nil
}

// run is the main event-handling loop for the watcher. It should be run in a
// separate goroutine.
func (w *Watcher) run(delay time.Duration, handler func([]protocol.FileEvent, error)) {
	defer w.runners.Done()

	// timer is used to debounce events.
	timer := time.NewTimer(delay)
	defer timer.Stop()

	for {
		select {
		case <-w.stop:
			return

		case <-timer.C:
			if events := w.drainEvents(); len(events) > 0 {
				handler(events, nil)
			}
			timer.Reset(delay)

		case err, ok := <-w.watcher.Errors:
			// When the watcher is closed, its Errors channel is closed, which
			// unblocks this case. We continue to the next loop iteration,
			// allowing the <-w.stop case to handle the shutdown.
			if !ok {
				continue
			}
			if err != nil {
				handler(nil, err)
			}

		case err, ok := <-w.errs:
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
			// fsnotify.Event should not be handled concurrently, to preserve their
			// original order. For example, if a file is deleted and recreated,
			// concurrent handling could process the events in reverse order.
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

			done, release := w.addWatchHandle(path)
			if done == nil { // file watcher closing
				return filepath.SkipAll
			}
			defer release()

			return w.watchDir(path, done)
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
		// Use watchHandles to determine if the deleted item was a directory.
		isDir = w.isWatchedDir(path)
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
			// The [fsnotify.Watcher] removes the watch for us.
			// fsnotify/fsnotify#268
			w.removeWatchHandle(path)

			// TODO(hxjiang): Directory removal events from some LSP clients may
			// not include corresponding removal events for child files and
			// subdirectories. Should we do some filtering when adding the dir
			// deletion event to the events slice.
			return &protocol.FileEvent{
				URI:  protocol.URIFromPath(path),
				Type: protocol.Deleted,
			}
		case event.Op.Has(fsnotify.Create):
			// This watch is added asynchronously to prevent a potential
			// deadlock on Windows. See fsnotify/fsnotify#502.
			// Error encountered will be sent to internal error channel.
			if done, release := w.addWatchHandle(path); done != nil {
				go func() {
					w.errs <- w.watchDir(path, done)

					// Only release after the error is sent.
					release()
				}()
			}

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

// watchDir registers a watch for a directory, retrying with backoff if it fails.
// It can be canceled by calling removeWatchHandle.
// Returns nil on success or cancellation; otherwise, the last error after all
// retries.
func (w *Watcher) watchDir(path string, done chan struct{}) error {
	// On darwin, watching a directory will fail if it contains broken symbolic
	// links. This state can occur temporarily during operations like a git
	// branch switch. To handle this, we retry multiple times with exponential
	// backoff, allowing time for the symbolic link's target to be created.

	// TODO(hxjiang): Address a race condition where file or directory creations
	// under current directory might be missed between the current directory
	// creation and the establishment of the file watch.
	//
	// To fix this, we should:
	// 1. Retrospectively check for and trigger creation events for any new
	// files/directories.
	// 2. Recursively add watches for any newly created subdirectories.
	var (
		delay = 500 * time.Millisecond
		err   error
	)

	for i := range 5 {
		if i > 0 {
			select {
			case <-time.After(delay):
				delay *= 2
			case <-done:
				return nil // cancelled
			}
		}
		// This function may block due to fsnotify/fsnotify#502.
		err = w.watcher.Add(path)
		if afterAddHook != nil {
			afterAddHook(path, err)
		}
		if err == nil {
			break
		}
	}

	return err
}

var afterAddHook func(path string, err error)

// addWatchHandle registers a new directory watch.
// The returned 'done' channel should be used to signal cancellation of a
// pending watch, the release function should be called once watch registration
// is done.
// It returns nil if the watcher is already closing.
func (w *Watcher) addWatchHandle(path string) (done chan struct{}, release func()) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.dirCancel == nil { // file watcher is closing.
		return nil, nil
	}

	done = make(chan struct{})
	w.dirCancel[path] = done

	w.watchers.Add(1)

	return done, w.watchers.Done
}

// removeWatchHandle removes the handle for a directory watch and cancels any
// pending watch attempt for that path.
func (w *Watcher) removeWatchHandle(path string) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if done, ok := w.dirCancel[path]; ok {
		delete(w.dirCancel, path)
		close(done)
	}
}

// isWatchedDir reports whether the given path has a watch handle, meaning it is
// a directory the watcher is managing.
func (w *Watcher) isWatchedDir(path string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()

	_, isDir := w.dirCancel[path]
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

func (w *Watcher) drainEvents() []protocol.FileEvent {
	w.mu.Lock()
	events := w.events
	w.events = nil
	w.mu.Unlock()

	return events
}

// Close shuts down the watcher, waits for the internal goroutine to terminate,
// and returns any final error.
func (w *Watcher) Close() error {
	// Set dirCancel to nil which prevent any future watch attempts.
	w.mu.Lock()
	dirCancel := w.dirCancel
	w.dirCancel = nil
	w.mu.Unlock()

	// Cancel any ongoing watch registration.
	for _, ch := range dirCancel {
		close(ch)
	}

	// Wait for all watch registration goroutines to finish, including their
	// error handling. This ensures that:
	// - All [Watcher.watchDir] goroutines have exited and it's error is sent
	//   to the internal error channel. So it is safe to close the internal
	//   error channel.
	// - There are no ongoing [fsnotify.Watcher.Add] calls, so it is safe to
	//   close the fsnotify watcher (see fsnotify/fsnotify#704).
	w.watchers.Wait()
	close(w.errs)

	err := w.watcher.Close()

	// Wait for the main run loop to terminate.
	close(w.stop)
	w.runners.Wait()

	return err
}
