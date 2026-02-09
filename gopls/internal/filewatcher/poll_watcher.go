// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package filewatcher

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io/fs"
	"log/slog"
	"path/filepath"
	"sync"
	"time"

	"golang.org/x/tools/gopls/internal/filecache"
	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/gopls/internal/util/bug"
	"golang.org/x/tools/gopls/internal/util/frob"
	"golang.org/x/tools/gopls/internal/util/moremaps"
	"golang.org/x/tools/internal/event"
)

/*
Design Notes:
The pollWatcher provides a portable, polling-based alternative to fsnotify.
Key lessons and design decisions from its implementation:

1. Persistence: It leverages the gopls machine-global filecache to persist
   file tree state across sessions, keyed by the hash of the root directory.
2. Adaptive Polling: It uses an adaptive backoff timer that accelerates
   (to 2s) when user activity is signaled via Poke() and slows down
   (up to 1m) when the file system is idle.
3. Synchronous Baseline: WatchDir performs a synchronous initial scan if
   no cached state exists. This acts as a barrier, ensuring that any
   subsequent file system changes are correctly detected as events.
4. Coalescing: Unlike fsnotify, polling naturally coalesces rapid sequences
   of events (e.g., Create + Change) into a single event based on the
   state difference between scans.
5. Multi-root: The watcher supports multiple independent root directories,
   each with its own independent state and persistence.
*/

// NewPollWatcher creates a new watcher that actively polls the file tree to
// detect changes. It uses an adaptive back-off strategy to reduce scans of the
// file tree and save battery; it is thus only eventually consistent.
func NewPollWatcher(interval time.Duration, log *slog.Logger, onEvents func([]protocol.FileEvent), onError func(error)) *pollWatcher {
	w := &pollWatcher{
		log:      log,
		onEvents: onEvents,
		onError:  onError,
		ctx:      context.Background(),
		stop:     make(chan struct{}),
		poke:     make(chan struct{}, 1),
		roots:    make(map[string]*promise[fileState]),
		interval: interval,
	}
	w.loops.Go(w.loop)
	return w
}

type pollWatcher struct {
	log      *slog.Logger
	onEvents func([]protocol.FileEvent)
	onError  func(error)
	interval time.Duration // polling interval

	// TODO(hxjiang): accept ctx from constructor and use ctx.Done() for Close.
	ctx context.Context

	stop  chan struct{}  // closed by Close to terminate [pollWatcher.loop] go routine
	loops sync.WaitGroup // counts the number of active [pollWatcher.loop] goroutine (max 1)

	poke chan struct{} // signals user activity

	mu    sync.Mutex                     // guards field below
	roots map[string]*promise[fileState] // clean root dir -> last known state.
}

type promise[T any] struct {
	value T
	err   error
	ready chan struct{}
}

func (p *promise[T]) isReady() bool {
	select {
	case <-p.ready:
		// The channel is closed.
		// A closed channel yields infinite zero-values without blocking.
		return true
	default:
		// The channel is still open, meaning the work isn't done.
		return false
	}
}

// fileState maps file paths (relative to a specific root directory) to
// their corresponding metadata.
type fileState map[string]fileInfo

// fileInfo is a frob-serializable record of the information returned
// by stat for a single directory entry.
type fileInfo struct {
	ModTime int64 // as defined by Time.UnixNano
	Size    int64
	IsDir   bool
}

func (w *pollWatcher) WatchDir(dir string) error {
	// TODO(hxjiang): prevent watching for a dir if the parent dir is already
	// being watched.

	dir = filepath.Clean(dir)

	w.mu.Lock()
	p, ok := w.roots[dir]
	if !ok || p.isReady() && p.err != nil {
		// First goroutine who try to watch "dir" or who observed the previous
		// attempt failed: this goroutine does the work.

		// Put /replace with a promise as a place holder
		p = &promise[fileState]{
			ready: make(chan struct{}),
		}
		w.roots[dir] = p
		w.mu.Unlock()

		p.value, p.err = w.watchDir(dir)
		close(p.ready)

		// Trigger a scan soon to detect subsequent changes.
		if p.err == nil {
			w.Poke()
		}
	} else {
		w.mu.Unlock()

		// Some other goroutine got there first.
		<-p.ready
	}

	// Inv: promise is ready
	return p.err
}

func (w *pollWatcher) watchDir(dir string) (fileState, error) {
	state, err := w.loadState(dir)

	// Cache miss: perform a synchronous scan to establish a baseline.
	if err != nil {
		_, newState, err := scan(dir, nil)
		if err != nil {
			return nil, err
		}
		state = newState
		w.saveState(dir, state)
	}

	return state, nil
}

func (w *pollWatcher) Close() error {
	close(w.stop)
	w.loops.Wait()
	return nil
}

func (w *pollWatcher) Poke() {
	select {
	case w.poke <- struct{}{}:
	default:
	}
}

// loop scans the tree periodically, using adaptive backoff, until the watcher
// is closed.
//
// A call to [pollWatcher.Poke] interrupts any long sleep and resets the timer
// to the fast polling interval.
func (w *pollWatcher) loop() {
	delay := w.interval
	timer := time.NewTimer(delay)
	defer timer.Stop()

	for {
		select {
		case <-w.stop:
			return

		case <-w.poke:
			delay = w.interval
			timer.Reset(delay)

		case <-timer.C:
			w.mu.Lock()
			roots := moremaps.KeySlice(w.roots)
			w.mu.Unlock()

			changed := false
			// TODO(hxjiang): run "scan" in parallel.
			for _, root := range roots {
				w.mu.Lock()
				p, ok := w.roots[root]
				w.mu.Unlock()
				if !ok {
					continue // Root removed (not possible as we don't support RemoveWatch)
				}
				if !p.isReady() {
					continue // Initial scan is undergoing
				}
				if p.err != nil {
					continue // Initial scan failed
				}

				changes, newState, err := scan(root, p.value)
				if err != nil {
					if w.onError != nil {
						w.onError(err)
					}
					continue
				}

				w.mu.Lock()
				if _, ok := w.roots[root]; ok {
					w.roots[root].value = newState
				}
				w.mu.Unlock()

				if len(changes) > 0 {
					w.onEvents(changes)
					w.saveState(root, newState)
					changed = true
				} else if p.value == nil {
					// Initial baseline established, save it so next run has a comparison.
					w.saveState(root, newState)
				}
			}

			if changed {
				// If changes found, keep polling fast for a bit.
				delay = w.interval
			} else {
				// No changes, backoff.
				delay = min(delay*2, 2*time.Hour)
			}
			timer.Reset(delay)
		}
	}
}

// scan walks the file tree for the given root directory and compares its
// current state against the provided oldState, returning a coalesced list
// of file system events (Created, Changed, Deleted) and the new state map.
//
// This method is concurrency safe. It does not mutate the watcher's internal
// state.
//
// To prevent triggering massive workspace reloads in the LSP, scan explicitly
// ignores modification time changes on the root directory itself.
func scan(root string, oldState fileState) ([]protocol.FileEvent, fileState, error) {
	var (
		newState = make(fileState)
		events   []protocol.FileEvent
	)
	addEvent := func(typ protocol.FileChangeType, path string) {
		events = append(events, protocol.FileEvent{
			URI:  protocol.URIFromPath(path),
			Type: typ,
		})
	}
	err := filepath.WalkDir(root, func(path string, dirent fs.DirEntry, err error) error {
		if err != nil {
			// Permission errors or disappearing files are ignored during walk.
			return nil
		}
		if path == root {
			// Skip the root directory itself. We are interested in its contents.
			// This avoids emitting a "Changed" event for the root whenever a
			// file is added or removed.
			return nil
		}
		if dirent.IsDir() && skipDir(dirent.Name()) {
			return filepath.SkipDir
		}
		if !dirent.IsDir() && skipFile(dirent.Name()) {
			return nil
		}

		info, err := dirent.Info()
		if err != nil {
			return nil
		}

		newInfo := fileInfo{
			ModTime: info.ModTime().UnixNano(),
			Size:    info.Size(),
			IsDir:   dirent.IsDir(),
		}
		newState[path] = newInfo

		if oldState == nil { // Initial population, no events.
			return nil
		}

		if oldInfo, ok := oldState[path]; ok {
			if oldInfo != newInfo {
				addEvent(protocol.Changed, path)
			}
		} else {
			addEvent(protocol.Created, path)
		}
		return nil
	})
	if err != nil {
		return nil, nil, err
	}

	for path := range oldState {
		if _, ok := newState[path]; !ok {
			addEvent(protocol.Deleted, path)
		}
	}

	return events, newState, nil
}

// -- filecache --

func (w *pollWatcher) loadState(root string) (fileState, error) {
	key := cacheKey(root)
	data, err := filecache.Get(filewatcherKind, key)
	if err != nil {
		if err != filecache.ErrNotFound {
			bug.Reportf("internal error reading shared cache: %v", err)
		}
		return nil, err
	}

	var state fileState
	codec.Decode(data, &state)
	return state, nil
}

func (w *pollWatcher) saveState(root string, state fileState) {
	key := cacheKey(root)
	data := codec.Encode(state)
	if err := filecache.Set(filewatcherKind, key, data); err != nil {
		event.Error(w.ctx, fmt.Sprintf("storing file watcher state data for %s", root), err)
	}
}

const filewatcherKind = "filewatcher"

var codec = frob.CodecFor[fileState]()

func cacheKey(root string) [32]byte {
	return sha256.Sum256([]byte(root))
}
