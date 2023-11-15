// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package cache

import (
	"context"
	"fmt"
	"os"
	"reflect"
	"strings"
	"sync"
	"time"

	"golang.org/x/tools/gopls/internal/file"
	"golang.org/x/tools/internal/event"
	"golang.org/x/tools/internal/event/keys"
	"golang.org/x/tools/internal/imports"
)

type importsState struct {
	ctx context.Context

	mu                     sync.Mutex
	processEnv             *imports.ProcessEnv
	cacheRefreshDuration   time.Duration
	cacheRefreshTimer      *time.Timer
	cachedModFileHash      file.Hash
	cachedBuildFlags       []string
	cachedDirectoryFilters []string
}

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

	// view.goEnv is immutable -- changes make a new view. Options can change.
	// We can't compare build flags directly because we may add -modfile.
	localPrefix := snapshot.Options().Local
	currentBuildFlags := snapshot.Options().BuildFlags
	currentDirectoryFilters := snapshot.Options().DirectoryFilters
	changed := !reflect.DeepEqual(currentBuildFlags, s.cachedBuildFlags) ||
		snapshot.Options().VerboseOutput != (s.processEnv.Logf != nil) ||
		modFileHash != s.cachedModFileHash ||
		!reflect.DeepEqual(snapshot.Options().DirectoryFilters, s.cachedDirectoryFilters)

	// If anything relevant to imports has changed, clear caches and
	// update the processEnv. Clearing caches blocks on any background
	// scans.
	if changed {
		if err := populateProcessEnvFromSnapshot(ctx, s.processEnv, snapshot); err != nil {
			return err
		}

		if resolver, err := s.processEnv.GetResolver(); err == nil {
			if modResolver, ok := resolver.(*imports.ModuleResolver); ok {
				modResolver.ClearForNewMod()
			}
		}

		s.cachedModFileHash = modFileHash
		s.cachedBuildFlags = currentBuildFlags
		s.cachedDirectoryFilters = currentDirectoryFilters
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
		LocalPrefix: localPrefix,
	}

	if err := fn(ctx, opts); err != nil {
		return err
	}

	if s.cacheRefreshTimer == nil {
		// Don't refresh more than twice per minute.
		delay := 30 * time.Second
		// Don't spend more than a couple percent of the time refreshing.
		if adaptive := 50 * s.cacheRefreshDuration; adaptive > delay {
			delay = adaptive
		}
		s.cacheRefreshTimer = time.AfterFunc(delay, s.refreshProcessEnv)
	}

	return nil
}

// populateProcessEnvFromSnapshot sets the dynamically configurable fields for
// the view's process environment. Assumes that the caller is holding the
// importsState mutex.
func populateProcessEnvFromSnapshot(ctx context.Context, pe *imports.ProcessEnv, snapshot *Snapshot) error {
	ctx, done := event.Start(ctx, "cache.populateProcessEnvFromSnapshot")
	defer done()

	if snapshot.Options().VerboseOutput {
		pe.Logf = func(format string, args ...interface{}) {
			event.Log(ctx, fmt.Sprintf(format, args...))
		}
	} else {
		pe.Logf = nil
	}

	pe.WorkingDir = snapshot.view.goCommandDir.Path()
	pe.ModFlag = "readonly" // processEnv operations should not mutate the modfile
	pe.Env = map[string]string{}
	pe.BuildFlags = append([]string{}, snapshot.Options().BuildFlags...)
	env := append(append(os.Environ(), snapshot.Options().EnvSlice()...), "GO111MODULE="+snapshot.view.GO111MODULE())
	for _, kv := range env {
		split := strings.SplitN(kv, "=", 2)
		if len(split) != 2 {
			continue
		}
		pe.Env[split[0]] = split[1]
	}
	return nil
}

func (s *importsState) refreshProcessEnv() {
	ctx, done := event.Start(s.ctx, "cache.importsState.refreshProcessEnv")
	defer done()

	start := time.Now()

	s.mu.Lock()
	env := s.processEnv
	if resolver, err := s.processEnv.GetResolver(); err == nil {
		resolver.ClearForNewScan()
	}
	s.mu.Unlock()

	event.Log(s.ctx, "background imports cache refresh starting")
	if err := imports.PrimeCache(context.Background(), env); err == nil {
		event.Log(ctx, fmt.Sprintf("background refresh finished after %v", time.Since(start)))
	} else {
		event.Log(ctx, fmt.Sprintf("background refresh finished after %v", time.Since(start)), keys.Err.Of(err))
	}
	s.mu.Lock()
	s.cacheRefreshDuration = time.Since(start)
	s.cacheRefreshTimer = nil
	s.mu.Unlock()
}
