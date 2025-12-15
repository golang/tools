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
	"golang.org/x/tools/internal/robustio"
)

// ErrClosed is used when trying to operate on a closed Watcher.
var ErrClosed = errors.New("file watcher: watcher already closed")

// Watcher collects events from a [fsnotify.Watcher] and converts them into
// batched LSP [protocol.FileEvent]s.
type Watcher struct {
	logger *slog.Logger

	stop chan struct{}  // closed by Close to terminate run and process loop
	wg   sync.WaitGroup // counts the number of active run and process goroutines (max 2)

	ready chan struct{} // signals work to process

	watcher *fsnotify.Watcher

	mu sync.Mutex // guards all fields below

	// in is the queue of fsnotify events waiting to be processed.
	in []fsnotify.Event

	// out is the current batch of unsent file events, which will be sent when
	// the timer expires.
	out []protocol.FileEvent

	// knownDirs tracks all known directories to help distinguish between file
	// and directory deletion events.
	knownDirs map[string]struct{}
}

// New creates a new file watcher and starts its event-handling loop. The
// [Watcher.Close] method must be called to clean up resources.
//
// The provided event handler is called sequentially with a batch of file events,
// but the error handler is called concurrently. The watcher blocks until the
// handler returns, so the handlers should be fast and non-blocking.
func New(delay time.Duration, logger *slog.Logger, eventsHandler func([]protocol.FileEvent), errHandler func(error)) (*Watcher, error) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	w := &Watcher{
		logger:    logger,
		watcher:   watcher,
		knownDirs: make(map[string]struct{}),
		stop:      make(chan struct{}),
		ready:     make(chan struct{}, 1),
	}

	w.wg.Add(1)
	go w.run(eventsHandler, errHandler, delay)

	w.wg.Add(1)
	go w.process(errHandler)

	return w, nil
}

// run is the receiver and sender loop.
//
// As receiver, its primary responsibility is to drain events and errors from
// the fsnotify watcher as quickly as possible and enqueue events for processing
// by the process goroutine. This is critical to work around a potential
// fsnotify deadlock (see fsnotify/fsnotify#502).
//
// As sender, it manages a timer and flush events to the handler if there is
// no events captured for a period of time.
func (w *Watcher) run(eventsHandler func([]protocol.FileEvent), errHandler func(error), delay time.Duration) {
	defer w.wg.Done()

	timer := time.NewTimer(delay)
	defer timer.Stop()

	for {
		select {
		case <-w.stop:
			return

		case <-timer.C:
			// TODO(hxjiang): flush is triggered when there is no events captured
			// in a certain period of time, it may be better to flush it when the
			// w.in is completely empty.
			//
			// Currently, partial events may be emitted if a directory watch gets
			// stuck. While this does not affect correctness, it means events
			// might be sent to the client in multiple portions rather than a
			// single batch.
			w.mu.Lock()
			events := w.out
			w.out = nil
			w.mu.Unlock()

			if len(events) > 0 {
				eventsHandler(events)
			}

			timer.Reset(delay)

		case event, ok := <-w.watcher.Events:
			// The watcher closed. Continue the loop and let the <-w.stop case
			// handle the actual shutdown.
			if !ok {
				continue
			}

			// TODO(hxjiang): perform some filtering before we reset the timer
			// to avoid consistenly resetting the timer in a noisy file syestem,
			// or simply convert the event here.
			timer.Reset(delay)

			w.mu.Lock()
			w.in = append(w.in, event)
			w.mu.Unlock()

			w.signal()

		case err, ok := <-w.watcher.Errors:
			// The watcher closed. Continue the loop and let the <-w.stop case
			// handle the actual shutdown.
			if !ok {
				continue
			}

			errHandler(err)
		}
	}
}

// process is a worker goroutine that converts raw fsnotify events from queue
// and handles the potentially blocking work of watching new directories. It is
// the counterpart to the run goroutine.
func (w *Watcher) process(errHandler func(error)) {
	defer w.wg.Done()

	for {
		select {
		case <-w.stop:
			return

		case <-w.ready:
			w.mu.Lock()
			events := w.in
			w.in = nil
			w.mu.Unlock()

			for _, event := range events {
				// File watcher is closing, drop any remaining work.
				select {
				case <-w.stop:
					return
				default:
				}

				// fsnotify does not guarantee clean filepaths.
				event.Name = filepath.Clean(event.Name)

				// fsnotify.Event should not be handled concurrently, to preserve their
				// original order. For example, if a file is deleted and recreated,
				// concurrent handling could process the events in reverse order.
				e, isDir := w.convertEvent(event)
				if e == (protocol.FileEvent{}) {
					continue
				}

				var synthesized []protocol.FileEvent // synthesized create events

				if isDir {
					switch e.Type {
					case protocol.Created:
						// Walks the entire directory tree, synthesizes create
						// events for its contents, and establishes watches for
						// subdirectories. This recursive, pre-order traversal
						// guarantees a logical event sequence: parent directory
						// creation events always precede those of their children.
						//
						// For example, consider a creation event for directory
						// a, and suppose a has contents [a/b, a/b/c, a/c, a/c/d].
						// The effective events will be:
						//
						//     CREATE a
						//     CREATE a/b
						//     CREATE a/b/c
						//     CREATE a/c
						//     CREATE a/c/d
						w.walkDirWithRetry(event.Name, errHandler, func(path string, isDir bool) error {
							if path != event.Name {
								synthesized = append(synthesized, protocol.FileEvent{
									URI:  protocol.URIFromPath(path),
									Type: protocol.Created,
								})
							}

							if isDir {
								return w.watchDir(path)
							} else {
								return nil
							}
						})

					case protocol.Deleted:
						// Upon removal, we only need to remove the entries from
						// the map. The [fsnotify.Watcher] removes the watch for
						// us. fsnotify/fsnotify#268
						w.mu.Lock()
						delete(w.knownDirs, event.Name)
						w.mu.Unlock()
					default:
						// convertEvent enforces that dirs are only Created or Deleted.
						panic("impossible")
					}
				}

				// Discovered events must be appended to the 'out' slice atomically.
				// This ensures that at any point, the slice contains a logically
				// correct (maybe slightly outdated) batch of file events that is
				// ready to be flushed.
				w.mu.Lock()
				// Some systems emit duplicate change events in close
				// succession upon file modification. While the current
				// deduplication is naive and only handles immediate duplicates,
				// a more robust solution is needed.
				// https://github.com/fsnotify/fsnotify?tab=readme-ov-file#why-do-i-get-many-chmod-events
				//
				// TODO(hxjiang): Enhance deduplication. The current batching of
				// events means all duplicates, regardless of proximity, should
				// be removed. Consider checking the entire buffered slice or
				// using a map for this.
				if len(w.out) == 0 || w.out[len(w.out)-1] != e {
					w.out = append(w.out, e)
				}
				w.out = append(w.out, synthesized...) // synthesized events are guaranteed to be unique
				w.mu.Unlock()
			}
		}
	}
}

// signal notifies the process goroutine that events are added to the queue and
// ready for handling.
func (w *Watcher) signal() {
	select {
	case w.ready <- struct{}{}:
	default:
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

// skipFile reports whether the file should be skipped.
func skipFile(fileName string) bool {
	switch strings.TrimPrefix(filepath.Ext(fileName), ".") {
	case "go", "mod", "sum", "work", "s":
		return false
	default:
		return true
	}
}

// WatchDir walks through the directory and all its subdirectories, adding
// them to the watcher.
func (w *Watcher) WatchDir(path string) error {
	return filepath.WalkDir(filepath.Clean(path), func(path string, d fs.DirEntry, err error) error {
		if d.IsDir() {
			if skipDir(d.Name()) {
				return filepath.SkipDir
			}

			return w.watchDir(path)
		}
		return nil
	})
}

// convertEvent translates an [fsnotify.Event] into a [protocol.FileEvent].
// It returns the translated event and a boolean indicating if the path was a
// directory. For directories, the event Type is either Created or Deleted.
// It returns empty event for events that should be ignored.
func (w *Watcher) convertEvent(event fsnotify.Event) (_ protocol.FileEvent, isDir bool) {
	// Determine if the event is for a directory.
	if info, err := os.Stat(event.Name); err == nil {
		isDir = info.IsDir()
	} else if os.IsNotExist(err) {
		// Upon deletion, the file/dir has been removed. fsnotify does not
		// provide information regarding the deleted item.
		// Use the set of known directories to determine if the deleted item was a directory.
		isDir = w.isWatchedDir(event.Name)
	} else {
		// If statting failed, something is wrong with the file system.
		// Log and move on.
		if w.logger != nil {
			w.logger.Error("failed to stat path, skipping event as its type (file/dir) is unknown", "path", event.Name, "err", err)
		}
		return protocol.FileEvent{}, false
	}

	// Filter out events for directories and files that are not of interest.
	if isDir && skipDir(filepath.Base(event.Name)) {
		return protocol.FileEvent{}, true
	}
	if !isDir && skipFile(filepath.Base(event.Name)) {
		return protocol.FileEvent{}, false
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
		// TODO(hxjiang): Directory removal events from some LSP clients may
		// not include corresponding removal events for child files and
		// subdirectories. Should we do some filtering when adding the dir
		// deletion event to the events slice.
		t = protocol.Deleted
	case event.Op.Has(fsnotify.Create):
		t = protocol.Created
	case event.Op.Has(fsnotify.Write):
		if isDir {
			return protocol.FileEvent{}, isDir // ignore dir write events
		}
		t = protocol.Changed
	default:
		return protocol.FileEvent{}, isDir // ignore the rest of the events
	}

	return protocol.FileEvent{
		URI:  protocol.URIFromPath(event.Name),
		Type: t,
	}, isDir
}

// watchDir registers a watch for a directory, retrying with backoff if it fails.
//
// Returns nil on success or watcher closing; otherwise, the last error after
// all retries.
func (w *Watcher) watchDir(path string) error {
	w.mu.Lock()
	w.knownDirs[path] = struct{}{}
	w.mu.Unlock()

	// On darwin, watching a directory will fail if it contains broken symbolic
	// links. This state can occur temporarily during operations like a git
	// branch switch. To handle this, we retry multiple times with exponential
	// backoff, allowing time for the symbolic link's target to be created.
	var (
		delay = 500 * time.Millisecond
		err   error
	)

	for i := range 5 {
		if i > 0 {
			select {
			case <-time.After(delay):
				delay *= 2
			case <-w.stop:
				return nil
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

// isWatchedDir reports whether the given path is a known directory that
// the watcher is managing.
func (w *Watcher) isWatchedDir(path string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()

	_, isDir := w.knownDirs[path]
	return isDir
}

// Close shuts down the watcher, waits for the internal goroutine to terminate,
// and returns any final error.
func (w *Watcher) Close() error {
	// Wait for fsnotify' watcher to terminate.
	err := w.watcher.Close()

	// Wait for run and process loop to terminate. It's important to stop the
	// run and process loop the last place because we don't know whether
	// fsnotify's watcher expect us to keep consuming events or errors from
	// [fsnotify.Watcher.Events] and [fsnotify.Watcher.Errors] while it's being
	// closed.
	// To avoid any potential deadlock, have the channel receiver running until
	// the last minute.
	close(w.stop)
	w.wg.Wait()

	return err
}

// walkDir calls fn against current path and recursively descends path for each
// file or directory of our interest.
func (w *Watcher) walkDir(path string, isDir bool, errHandler func(error), fn func(path string, isDir bool) error) {
	if err := fn(path, isDir); err != nil {
		errHandler(err)
		return
	}

	if !isDir {
		return
	}

	entries, err := tryFSOperation(w.stop, func() ([]fs.DirEntry, error) {
		// ReadDir may fail due because other processes may be actively
		// modifying the watched dir see golang/go#74820.
		// TODO(hxjiang): consider adding robustio.ReadDir.
		return os.ReadDir(path)
	})
	if err != nil {
		if err != ErrClosed {
			errHandler(err)
		}
		return
	}

	for _, e := range entries {
		if e.IsDir() && skipDir(e.Name()) {
			continue
		}
		if !e.IsDir() && skipFile(e.Name()) {
			continue
		}

		w.walkDir(filepath.Join(path, e.Name()), e.IsDir(), errHandler, fn)
	}
}

// walkDirWithRetry walks the file tree rooted at root, calling fn for each
// file or directory of our interest in the tree, including root.
//
// All errors that arise visiting directories or files will be reported to the
// provided error handler function. If an error is encountered visiting a
// directory, that entire subtree will be skipped.
//
// walkDirWithRetry does not follow symbolic links.
//
// It is used instead of [filepath.WalkDir] because it provides control over
// retry behavior when reading a directory fails. If [os.ReadDir] fails with an
// ephemeral error, it is retried multiple times with exponential backoff.
//
// TODO(hxjiang): call walkDirWithRetry in WalkDir.
func (w *Watcher) walkDirWithRetry(root string, errHandler func(error), fn func(path string, isDir bool) error) {
	info, err := tryFSOperation(w.stop, func() (os.FileInfo, error) {
		return os.Lstat(root) // [os.Lstat] does not follow symlink.
	})
	if err != nil {
		if err != ErrClosed {
			errHandler(err)
		}
		return
	}

	w.walkDir(root, info.IsDir(), errHandler, fn)
}

// tryFSOperation executes a function `op` with retry logic, making it resilient
// to transient errors. It attempts the operation up to 5 times with exponential
// backoff. Retries occur only if the error is ephemeral.
//
// The operation can be interrupted by closing the `stop` channel, in which case
// it returns [ErrClosed].
func tryFSOperation[Result any](stop <-chan struct{}, op func() (Result, error)) (Result, error) {
	var (
		delay = 50 * time.Millisecond
		err   error
	)

	for i := range 5 {
		if i > 0 {
			select {
			case <-time.After(delay):
				delay *= 2
			case <-stop:
				var zero Result
				return zero, ErrClosed
			}
		}

		var res Result
		res, err = op()

		if robustio.IsEphemeralError(err) {
			continue
		} else {
			return res, err
		}
	}

	var zero Result
	return zero, err // return last error encountered
}
