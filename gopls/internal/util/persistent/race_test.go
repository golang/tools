// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build race

package persistent

import (
	"context"
	"maps"
	"testing"
	"time"

	"golang.org/x/sync/errgroup"
)

// TestConcurrency exercises concurrent map access.
// It doesn't assert anything, but it runs under the race detector.
func TestConcurrency(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	var orig Map[int, int] // maps subset of [0-10] to itself (values aren't interesting)
	for i := range 10 {
		orig.Set(i, i, func(k, v any) { /* just for good measure*/ })
	}
	g, ctx := errgroup.WithContext(ctx)
	const N = 10 // concurrency level
	g.SetLimit(N)
	for range N {
		g.Go(func() error {
			// Each thread has its own clone of the original,
			// sharing internal structures. Each map is accessed
			// only by a single thread; the shared data is immutable.
			m := orig.Clone()

			// Run until the timeout.
			for ctx.Err() == nil {
				for i := range 1000 {
					key := i % 10

					switch {
					case i%2 == 0:
						_, _ = m.Get(key)
					case i%11 == 0:
						m.Set(key, key, func(key, value any) {})
					case i%13 == 0:
						_ = maps.Collect(m.All())
					case i%17 == 0:
						_ = m.Delete(key)
					case i%19 == 0:
						_ = m.Keys()
					case i%31 == 0:
						_ = m.String()
					case i%23 == 0:
						_ = m.Clone()
					}
					// Don't call m.Clear(), as it would
					// disentangle the various maps from each other.
				}
			}
			return nil
		})
	}
	g.Wait() // no errors
}
