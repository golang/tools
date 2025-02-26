// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package cache

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"golang.org/x/tools/gopls/internal/file"
	"golang.org/x/tools/gopls/internal/label"
	"golang.org/x/tools/internal/event"
	"golang.org/x/tools/internal/event/keys"
	"golang.org/x/tools/internal/imports"
	"golang.org/x/tools/internal/modindex"
)

// refreshTimer implements delayed asynchronous refreshing of state.
//
// See the [refreshTimer.schedule] documentation for more details.
type refreshTimer struct {
	mu        sync.Mutex
	duration  time.Duration
	timer     *time.Timer
	refreshFn func()
}

// newRefreshTimer constructs a new refresh timer which schedules refreshes
// using the given function.
func newRefreshTimer(refresh func()) *refreshTimer {
	return &refreshTimer{
		refreshFn: refresh,
	}
}

// stop stops any future scheduled refresh.
func (t *refreshTimer) stop() {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.timer != nil {
		t.timer.Stop()
		t.timer = nil
		t.refreshFn = nil // release resources
	}
}

// schedule schedules the refresh function to run at some point in the future,
// if no existing refresh is already scheduled.
//
// At a minimum, scheduled refreshes are delayed by 30s, but they may be
// delayed longer to keep their expected execution time under 2% of wall clock
// time.
func (t *refreshTimer) schedule() {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.timer == nil {
		// Don't refresh more than twice per minute.
		// Don't spend more than ~2% of the time refreshing.
		delay := max(30*time.Second, 50*t.duration)
		t.timer = time.AfterFunc(delay, func() {
			start := time.Now()
			t.mu.Lock()
			refreshFn := t.refreshFn
			t.mu.Unlock()
			if refreshFn != nil { // timer may be stopped.
				refreshFn()
				t.mu.Lock()
				t.duration = time.Since(start)
				t.timer = nil
				t.mu.Unlock()
			}
		})
	}
}

// A sharedModCache tracks goimports state for GOMODCACHE directories
// (each session may have its own GOMODCACHE).
//
// This state is refreshed independently of view-specific imports state.
type sharedModCache struct {
	mu     sync.Mutex
	caches map[string]*imports.DirInfoCache // GOMODCACHE -> cache content; never invalidated
	// TODO(rfindley): consider stopping these timers when the session shuts down.
	timers map[string]*refreshTimer // GOMODCACHE -> timer
}

func (c *sharedModCache) dirCache(dir string) *imports.DirInfoCache {
	c.mu.Lock()
	defer c.mu.Unlock()

	cache, ok := c.caches[dir]
	if !ok {
		cache = imports.NewDirInfoCache()
		c.caches[dir] = cache
	}
	return cache
}

// refreshDir schedules a refresh of the given directory, which must be a
// module cache.
func (c *sharedModCache) refreshDir(ctx context.Context, dir string, logf func(string, ...any)) {
	cache := c.dirCache(dir)

	c.mu.Lock()
	defer c.mu.Unlock()
	timer, ok := c.timers[dir]
	if !ok {
		timer = newRefreshTimer(func() {
			_, done := event.Start(ctx, "cache.sharedModCache.refreshDir", label.Directory.Of(dir))
			defer done()
			imports.ScanModuleCache(dir, cache, logf)
		})
		c.timers[dir] = timer
	}

	timer.schedule()
}

// importsState tracks view-specific imports state.
type importsState struct {
	ctx          context.Context
	modCache     *sharedModCache
	refreshTimer *refreshTimer

	mu                sync.Mutex
	processEnv        *imports.ProcessEnv
	cachedModFileHash file.Hash
}

// newImportsState constructs a new imports state for running goimports
// functions via [runProcessEnvFunc].
//
// The returned state will automatically refresh itself following a delay.
func newImportsState(backgroundCtx context.Context, modCache *sharedModCache, env *imports.ProcessEnv) *importsState {
	s := &importsState{
		ctx:        backgroundCtx,
		modCache:   modCache,
		processEnv: env,
	}
	s.refreshTimer = newRefreshTimer(s.refreshProcessEnv)
	s.refreshTimer.schedule()
	return s
}

// modcacheState holds a modindex.Index and controls its updates
type modcacheState struct {
	dir          string // GOMODCACHE
	refreshTimer *refreshTimer
	mu           sync.Mutex
	index        *modindex.Index
}

// newModcacheState constructs a new modcacheState for goimports.
// The returned state is automatically updated until [modcacheState.stopTimer] is called.
func newModcacheState(dir string) *modcacheState {
	s := &modcacheState{
		dir: dir,
	}
	s.index, _ = modindex.ReadIndex(dir)
	s.refreshTimer = newRefreshTimer(s.refreshIndex)
	go s.refreshIndex()
	return s
}

func (s *modcacheState) GetIndex() (*modindex.Index, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ix := s.index
	if ix == nil || len(ix.Entries) == 0 {
		var err error
		// this should only happen near the beginning of a session
		// (or in tests)
		ix, err = modindex.ReadIndex(s.dir)
		if err != nil {
			return nil, fmt.Errorf("ReadIndex %w", err)
		}
		if !testing.Testing() {
			return ix, nil
		}
		if ix == nil || len(ix.Entries) == 0 {
			err = modindex.Create(s.dir)
			if err != nil {
				return nil, fmt.Errorf("creating index %w", err)
			}
			ix, err = modindex.ReadIndex(s.dir)
			if err != nil {
				return nil, fmt.Errorf("read index after create %w", err)
			}
			s.index = ix
		}
	}
	return s.index, nil
}

func (s *modcacheState) refreshIndex() {
	ok, err := modindex.Update(s.dir)
	if err != nil || !ok {
		return
	}
	// read the new index
	s.mu.Lock()
	defer s.mu.Unlock()
	s.index, _ = modindex.ReadIndex(s.dir)
}

func (s *modcacheState) stopTimer() {
	s.refreshTimer.stop()
}

// stopTimer stops scheduled refreshes of this imports state.
func (s *importsState) stopTimer() {
	s.refreshTimer.stop()
}

// runProcessEnvFunc runs goimports.
//
// Any call to runProcessEnvFunc will schedule a refresh of the imports state
// at some point in the future, if such a refresh is not already scheduled. See
// [refreshTimer] for more details.
func (s *importsState) runProcessEnvFunc(ctx context.Context, snapshot *Snapshot, fn func(context.Context, *imports.Options) error) error {
	ctx, done := event.Start(ctx, "cache.importsState.runProcessEnvFunc")
	defer done()

	s.mu.Lock()
	defer s.mu.Unlock()

	// Find the hash of active mod files, if any. Using the unsaved content
	// is slightly wasteful, since we'll drop caches a little too often, but
	// the mod file shouldn't be changing while people are autocompleting.
	//
	// TODO(rfindley): consider instead hashing on-disk modfiles here.
	var modFileHash file.Hash
	for m := range snapshot.view.workspaceModFiles {
		fh, err := snapshot.ReadFile(ctx, m)
		if err != nil {
			return err
		}
		modFileHash.XORWith(fh.Identity().Hash)
	}

	// If anything relevant to imports has changed, clear caches and
	// update the processEnv. Clearing caches blocks on any background
	// scans.
	if modFileHash != s.cachedModFileHash {
		s.processEnv.ClearModuleInfo()
		s.cachedModFileHash = modFileHash
	}

	// Run the user function.
	opts := &imports.Options{
		// Defaults.
		AllErrors:   true,
		Comments:    true,
		Fragment:    true,
		FormatOnly:  false,
		TabIndent:   true,
		TabWidth:    8,
		Env:         s.processEnv,
		LocalPrefix: snapshot.Options().Local,
	}

	if err := fn(ctx, opts); err != nil {
		return err
	}

	// Refresh the imports resolver after usage. This may seem counterintuitive,
	// since it means the first ProcessEnvFunc after a long period of inactivity
	// may be stale, but in practice we run ProcessEnvFuncs frequently during
	// active development (e.g. during completion), and so this mechanism will be
	// active while gopls is in use, and inactive when gopls is idle.
	s.refreshTimer.schedule()

	// TODO(rfindley): the GOMODCACHE value used here isn't directly tied to the
	// ProcessEnv.Env["GOMODCACHE"], though they should theoretically always
	// agree. It would be better if we guaranteed this, possibly by setting all
	// required environment variables in ProcessEnv.Env, to avoid the redundant
	// Go command invocation.
	gomodcache := snapshot.view.folder.Env.GOMODCACHE
	s.modCache.refreshDir(s.ctx, gomodcache, s.processEnv.Logf)

	return nil
}

func (s *importsState) refreshProcessEnv() {
	ctx, done := event.Start(s.ctx, "cache.importsState.refreshProcessEnv")
	defer done()

	start := time.Now()

	s.mu.Lock()
	resolver, err := s.processEnv.GetResolver()
	s.mu.Unlock()
	if err != nil {
		event.Error(s.ctx, "failed to get import resolver", err)
		return
	}

	event.Log(s.ctx, "background imports cache refresh starting")
	resolver2 := resolver.ClearForNewScan()

	// Prime the new resolver before updating the processEnv, so that gopls
	// doesn't wait on an unprimed cache.
	if err := imports.PrimeCache(context.Background(), resolver2); err == nil {
		event.Log(ctx, fmt.Sprintf("background refresh finished after %v", time.Since(start)))
	} else {
		event.Log(ctx, fmt.Sprintf("background refresh finished after %v", time.Since(start)), keys.Err.Of(err))
	}

	s.mu.Lock()
	s.processEnv.UpdateResolver(resolver2)
	s.mu.Unlock()
}
