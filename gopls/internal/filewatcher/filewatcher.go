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

// FileWatcher collects events from a [fsnotify.Watcher] and converts them into
// batched LSP [protocol.FileEvent]s. Events are debounced and sent to the
// event channel after a configurable period of no new relevant activity.
type FileWatcher struct {
	logger *slog.Logger

	closed chan struct{}

	wg sync.WaitGroup

	mu sync.Mutex

	// watchedDirs keeps track of which directories are being watched by the
	// watcher, explicitly added via [fsnotify.Watcher.Add].
	watchedDirs map[string]bool
	watcher     *fsnotify.Watcher

	// events is the current batch of unsent [protocol.FileEvent]s, which will
	// be sent when the timer expires.
	events []protocol.FileEvent
}

// New creates a new FileWatcher and starts its event-handling loop. The
// [FileWatcher.Close] should be called to cleanup.
func New(delay time.Duration, logger *slog.Logger) (*FileWatcher, <-chan []protocol.FileEvent, <-chan error, error) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, nil, nil, err
	}
	w := &FileWatcher{
		logger:      logger,
		watcher:     watcher,
		watchedDirs: make(map[string]bool),
		closed:      make(chan struct{}),
	}

	eventsChan := make(chan []protocol.FileEvent)
	errorChan := make(chan error)

	w.wg.Add(1)
	go w.run(eventsChan, errorChan, delay)

	return w, eventsChan, errorChan, nil
}

// run is the main event-handling loop for the watcher. It should be run in a
// separate goroutine.
func (w *FileWatcher) run(events chan<- []protocol.FileEvent, errs chan<- error, delay time.Duration) {
	defer w.wg.Done()

	// timer is used to debounce events.
	timer := time.NewTimer(delay)
	defer timer.Stop()

	for {
		select {
		case <-w.closed:
			// File watcher should not send the remaining events to the receiver
			// because the client may not listening to the channel, could
			// result in blocking forever.
			//
			// Once close signal received, ErrorChan and EventsChan will be
			// closed. Exit the go routine to ensure no more value will be sent
			// through those channels.
			close(errs)
			close(events)
			return

		case <-timer.C:
			w.sendEvents(events)
			timer.Reset(delay)

		case err, ok := <-w.watcher.Errors:
			// When the watcher is closed, its Errors channel is closed, which
			// unblocks this case. We continue to the next loop iteration,
			// allowing the <-w.closed case to handle the shutdown.
			if !ok {
				continue
			}
			errs <- err

		case event, ok := <-w.watcher.Events:
			if !ok {
				continue
			}
			// FileWatcher should not handle the fsnotify.Event concurrently,
			// the original order should be preserved. E.g. if a file get
			// deleted and recreated, running concurrently may result it in
			// reverse order.
			//
			// Only reset the timer if an relevant event happened.
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
func (w *FileWatcher) WatchDir(path string) error {
	return filepath.WalkDir(filepath.Clean(path), func(path string, d fs.DirEntry, err error) error {
		if d.IsDir() {
			if skipDir(d.Name()) {
				return filepath.SkipDir
			}
			if err := w.watchDir(path); err != nil {
				// TODO(hxjiang): retry on watch failures.
				return filepath.SkipDir
			}
		}
		return nil
	})
}

// handleEvent converts a single [fsnotify.Event] to the corresponding
// [protocol.FileEvent].
// Returns nil if the input event is not relevant.
func (w *FileWatcher) handleEvent(event fsnotify.Event) *protocol.FileEvent {
	// fsnotify does not guarantee clean filepaths.
	path := filepath.Clean(event.Name)

	var isDir bool
	if info, err := os.Stat(path); err == nil {
		isDir = info.IsDir()
	} else {
		if os.IsNotExist(err) {
			// Upon deletion, the file/dir has been removed. fsnotify
			// does not provide information regarding the deleted item.
			// Use the watchedDirs to determine whether it's a dir.
			isDir = w.isDir(path)
		} else {
			// If statting failed, something is wrong with the file system.
			// Log and move on.
			if w.logger != nil {
				w.logger.Error("failed to stat path, skipping event as its type (file/dir) is unknown", "path", path, "err", err)
			}
			return nil
		}
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
			w.unwatchDir(path)

			// TODO(hxjiang): Directory removal events from some LSP clients may
			// not include corresponding removal events for child files and
			// subdirectories. Should we do some filtering when add the dir
			// deletion event to the events slice.
			return &protocol.FileEvent{
				URI:  protocol.URIFromPath(path),
				Type: protocol.Deleted,
			}
		case event.Op.Has(fsnotify.Create):
			// TODO(hxjiang): retry on watch failure.
			_ = w.watchDir(path)

			return &protocol.FileEvent{
				URI:  protocol.URIFromPath(path),
				Type: protocol.Created,
			}
		default:
			return nil
		}
	} else {
		// Only watch *.{go,mod,sum,work}
		switch strings.TrimPrefix(filepath.Ext(path), ".") {
		case "go", "mod", "sum", "work":
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

func (w *FileWatcher) watchDir(path string) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	// Dir with broken symbolic link can not be watched.
	// TODO(hxjiang): is it possible the files/dirs are
	// created before the watch is successfully registered.
	if err := w.watcher.Add(path); err != nil {
		return err
	}
	w.watchedDirs[path] = true
	return nil
}

func (w *FileWatcher) unwatchDir(path string) {
	w.mu.Lock()
	defer w.mu.Unlock()

	// Upon removal, we only need to remove the entries from the map
	// [fileWatcher.watchedDirPath].
	// The [fsnotify.Watcher] remove the watch for us.
	// fsnotify/fsnotify#268
	delete(w.watchedDirs, path)
}

func (w *FileWatcher) isDir(path string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()

	_, isDir := w.watchedDirs[path]
	return isDir
}

func (w *FileWatcher) addEvent(event protocol.FileEvent) {
	w.mu.Lock()
	defer w.mu.Unlock()

	// Some architectures emit duplicate change events in close
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

func (w *FileWatcher) sendEvents(eventsChan chan<- []protocol.FileEvent) {
	w.mu.Lock() // Guard the w.events read and write. Not w.EventChan.
	defer w.mu.Unlock()

	if len(w.events) != 0 {
		eventsChan <- w.events
		w.events = make([]protocol.FileEvent, 0)
	}
}

// Close shuts down the watcher, waits for the internal goroutine to terminate,
// and returns any final error.
func (w *FileWatcher) Close() error {
	w.mu.Lock()

	err := w.watcher.Close()
	// Wait for the go routine to finish. So all the channels will be closed and
	// all go routine will be terminated.
	close(w.closed)

	w.mu.Unlock()

	w.wg.Wait()

	return err
}
