// Copyright 2019 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package cache

import (
	"reflect"
	"strconv"
	"sync/atomic"

	"golang.org/x/tools/gopls/internal/protocol/command"
	"golang.org/x/tools/internal/imports"
	"golang.org/x/tools/internal/memoize"
)

// New Creates a new cache for gopls operation results, using the given file
// set, shared store, and session options.
//
// Both the fset and store may be nil, but if store is non-nil so must be fset
// (and they must always be used together), otherwise it may be possible to get
// cached data referencing token.Pos values not mapped by the FileSet.
func New(store *memoize.Store) *Cache {
	index := atomic.AddInt64(&cacheIndex, 1)

	if store == nil {
		store = &memoize.Store{}
	}

	c := &Cache{
		id:         strconv.FormatInt(index, 10),
		store:      store,
		memoizedFS: newMemoizedFS(),
		modCache: &sharedModCache{
			caches: make(map[string]*imports.DirInfoCache),
			timers: make(map[string]*refreshTimer),
		},
	}
	return c
}

// A Cache holds content that is shared across multiple gopls sessions.
type Cache struct {
	id string

	// store holds cached calculations.
	//
	// TODO(rfindley): at this point, these are not important, as we've moved our
	// content-addressable cache to the file system (the filecache package). It
	// is unlikely that this shared cache provides any shared value. We should
	// consider removing it, replacing current uses with a simpler futures cache,
	// as we've done for e.g. type-checked packages.
	store *memoize.Store

	// memoizedFS holds a shared file.Source that caches reads.
	//
	// Reads are invalidated when *any* session gets a didChangeWatchedFile
	// notification. This is fine: it is the responsibility of memoizedFS to hold
	// our best knowledge of the current file system state.
	*memoizedFS

	// modCache holds the
	modCache *sharedModCache
}

var cacheIndex, sessionIndex, viewIndex int64

func (c *Cache) ID() string                     { return c.id }
func (c *Cache) MemStats() map[reflect.Type]int { return c.store.Stats() }

// FileStats returns information about the set of files stored in the cache.
// It is intended for debugging only.
func (c *Cache) FileStats() (stats command.FileStats) {
	stats.Total, stats.Largest, stats.Errs = c.fileStats()
	return
}
