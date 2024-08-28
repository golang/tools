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

// ballast is a 100MB unused byte slice that exists only to reduce garbage
// collector CPU in small workspaces and at startup.
//
// The redesign of gopls described at https://go.dev/blog/gopls-scalability
// moved gopls to a model where it has a significantly smaller heap, yet still
// allocates many short-lived data structures during parsing and type checking.
// As a result, for some workspaces, particularly when opening a low-level
// package, the steady-state heap may be a small fraction of total allocation
// while rechecking the workspace, paradoxically causing the GC to consume much
// more CPU. For example, in one benchmark that analyzes the starlark
// repository, the steady-state heap was ~10MB, and the process of diagnosing
// the workspace allocated 100-200MB.
//
// The reason for this paradoxical behavior is that GC pacing
// (https://tip.golang.org/doc/gc-guide#GOGC) causes the collector to trigger
// at some multiple of the steady-state heap size, so a small steady-state heap
// causes GC to trigger sooner and more often when allocating the ephemeral
// structures.
//
// Allocating a 100MB ballast avoids this problem by ensuring a minimum heap
// size. The value of 100MB was chosen to be proportional to the in-memory
// cache in front the filecache package, and the throughput of type checking.
// Gopls already requires hundreds of megabytes of RAM to function.
//
// Note that while other use cases for a ballast were made obsolete by
// GOMEMLIMIT, ours is not. GOMEMLIMIT helps in cases where you have a
// containerized service and want to optimize its latency and throughput by
// taking advantage of available memory. However, in our case gopls is running
// on the developer's machine alongside other applications, and can have a wide
// range of memory footprints depending on the size of the user's workspace.
// Setting GOMEMLIMIT to too low a number would make gopls perform poorly on
// large repositories, and setting it to too high a number would make gopls a
// badly behaved tenant. Short of calibrating GOMEMLIMIT based on the user's
// workspace (an intractible problem), there is no way for gopls to use
// GOMEMLIMIT to solve its GC CPU problem.
//
// Because this allocation is large and occurs early, there is a good chance
// that rather than being recycled, it comes directly from the OS already
// zeroed, and since it is never accessed, the memory region may avoid being
// backed by pages of RAM. But see
// https://groups.google.com/g/golang-nuts/c/66d0cItfkjY/m/3NvgzL_sAgAJ
//
// For more details on this technique, see:
// https://blog.twitch.tv/en/2019/04/10/go-memory-ballast-how-i-learnt-to-stop-worrying-and-love-the-heap/
var ballast = make([]byte, 100*1e6)

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
