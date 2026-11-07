// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package cache

import (
	"context"
	"sync"
)

// A futureCache is a key-value store of "futures", which are values that might
// not yet be processed. By accessing values using [futureCache.get], the
// caller may share work with other goroutines that require the same key.
//
// This is a relatively common pattern, though this implementation includes the
// following two non-standard additions:
//
//  1. futures are cancellable and retryable. If the context being used to
//     compute the future is cancelled, it will abort the computation. If other
//     goroutes are awaiting the future, they will acquire the right to compute
//     it, and start anew.
//  2. futures may be either persistent or transient. Persistent futures are
//     the standard pattern: the results of the computation are preserved for
//     the lifetime of the cache. However, if the cache is transient
//     (persistent=false), the futures will be discarded once their value has
//     been passed to all awaiting goroutines.
//
// These specific extensions are used to implement the concurrency model of the
// [typeCheckBatch], which allows multiple operations to piggy-back on top of
// an ongoing type checking operation, requesting new packages asynchronously
// without unduly increasing the in-use memory required by the type checking
// pass.
type futureCache[K comparable, V any] struct {
	persistent bool

	mu    sync.Mutex
	cache map[K]*future[V]
}

// newFutureCache returns a futureCache that is ready to coordinate
// computations via [futureCache.get].
//
// If persistent is true, the results of these computations are stored for the
// lifecycle of cache. Otherwise, results are discarded after they have been
// passed to all awaiting goroutines.
func newFutureCache[K comparable, V any](persistent bool) *futureCache[K, V] {
	return &futureCache[K, V]{
		persistent: persistent,
		cache:      make(map[K]*future[V]),
	}
}

type future[V any] struct {
	// refs is the number of goroutines awaiting this future, to be used for
	// cleaning up transient cache entries.
	//
	// Guarded by futureCache.mu.
	refs int

	// done is closed when the future has been fully computed.
	done chan unit

	// acquire used to select an awaiting goroutine to run the computation.
	// acquire is 1-buffered, and initialized with one unit, so that the first
	// requester starts a computation. If that computation is cancelled, the
	// requester pushes the unit back to acquire, so that another goroutine may
	// execute the computation.
	acquire chan unit

	// v and err store the result of the computation, guarded by done.
	v   V
	err error
}

// cacheFunc is the type of a future computation function.
type cacheFunc[V any] func(context.Context) (V, error)

// get retrieves or computes the value corresponding to k.
//
// If the cache if persistent and the value has already been computed, get
// returns the result of the previous computation. Otherwise, get either starts
// a computation or joins an ongoing computation. If that computation is
// cancelled, get will reassign the computation to a new goroutine as long as
// there are awaiters.
//
// Once the computation completes, the result is passed to all awaiting
// goroutines. If the cache is transient (persistent=false), the corresponding
// cache entry is removed, and the next call to get will execute a new
// computation.
//
// It is therefore the responsibility of the caller to ensure that the given
// compute function is safely retryable, and always returns the same value.
func (c *futureCache[K, V]) get(ctx context.Context, k K, compute cacheFunc[V]) (V, error) {
	c.mu.Lock()
	f, ok := c.cache[k]
	if !ok {
		f = &future[V]{
			done:    make(chan unit),
			acquire: make(chan unit, 1),
		}
		f.acquire <- unit{} // make available for computation
		c.cache[k] = f
	}
	f.refs++
	c.mu.Unlock()

	defer func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		f.refs--
		if f.refs == 0 && !c.persistent {
			delete(c.cache, k)
		}
	}()

	var zero V
	select {
	case <-ctx.Done():
		return zero, ctx.Err()
	case <-f.done:
		return f.v, f.err
	case <-f.acquire:
	}

	v, err := compute(ctx)
	if err := ctx.Err(); err != nil {
		f.acquire <- unit{} // hand off work to the next requester
		return zero, err
	}

	f.v = v
	f.err = err
	close(f.done)
	return v, err
}
