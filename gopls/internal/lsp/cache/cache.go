// Copyright 2019 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package cache

import (
	"reflect"
	"strconv"
	"sync/atomic"

	"golang.org/x/tools/gopls/internal/lsp/command"
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
	}
	return c
}

// A Cache holds caching stores that are bundled together for consistency.
//
// TODO(rfindley): once fset and store need not be bundled together, the Cache
// type can be eliminated.
type Cache struct {
	id string

	store *memoize.Store

	*memoizedFS // implements source.FileSource
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
