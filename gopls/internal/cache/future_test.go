// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package cache

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/sync/errgroup"
)

func TestFutureCache_Persistent(t *testing.T) {
	c := newFutureCache[int, int](true)
	ctx := context.Background()

	var computed atomic.Int32
	compute := func(i int) cacheFunc[int] {
		return func(context.Context) (int, error) {
			computed.Add(1)
			return i, ctx.Err()
		}
	}

	testFutureCache(t, ctx, c, compute)

	// Since this cache is persistent, we should get exactly 10 computations,
	// since there are 10 distinct keys in [testFutureCache].
	if got := computed.Load(); got != 10 {
		t.Errorf("computed %d times, want 10", got)
	}
}

func TestFutureCache_Ephemeral(t *testing.T) {
	c := newFutureCache[int, int](false)
	ctx := context.Background()

	var computed atomic.Int32
	compute := func(i int) cacheFunc[int] {
		return func(context.Context) (int, error) {
			time.Sleep(1 * time.Millisecond)
			computed.Add(1)
			return i, ctx.Err()
		}
	}

	testFutureCache(t, ctx, c, compute)

	// Since this cache is ephemeral, we should get at least 30 computations,
	// since there are 10 distinct keys and three synchronous passes in
	// [testFutureCache].
	if got := computed.Load(); got < 30 {
		t.Errorf("computed %d times, want at least 30", got)
	} else {
		t.Logf("compute ran %d times", got)
	}
}

// testFutureCache starts 100 goroutines concurrently, indexed by j, each
// getting key j%10 from the cache. It repeats this three times, synchronizing
// after each.
//
// This is designed to exercise both concurrent and synchronous access to the
// cache.
func testFutureCache(t *testing.T, ctx context.Context, c *futureCache[int, int], compute func(int) cacheFunc[int]) {
	for range 3 {
		var g errgroup.Group
		for j := range 100 {
			mod := j % 10
			compute := compute(mod)
			g.Go(func() error {
				got, err := c.get(ctx, mod, compute)
				if err == nil && got != mod {
					t.Errorf("get() = %d, want %d", got, mod)
				}
				return err
			})
		}
		if err := g.Wait(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestFutureCache_Retrying(t *testing.T) {
	// This test verifies the retry behavior of cache entries,
	// by checking that cancelled work is handed off to the next awaiter.
	//
	// The setup is a little tricky: 10 goroutines are started, and the first 9
	// are cancelled whereas the 10th is allowed to finish. As a result, the
	// computation should always succeed with value 9.

	ctx := context.Background()

	for _, persistent := range []bool{true, false} {
		t.Run(fmt.Sprintf("persistent=%t", persistent), func(t *testing.T) {
			c := newFutureCache[int, int](persistent)

			var started atomic.Int32

			// compute returns a new cacheFunc that produces the value i, after the
			// provided done channel is closed.
			compute := func(i int, done <-chan struct{}) cacheFunc[int] {
				return func(ctx context.Context) (int, error) {
					started.Add(1)
					select {
					case <-ctx.Done():
						return 0, ctx.Err()
					case <-done:
						return i, nil
					}
				}
			}

			// goroutines are either cancelled, or allowed to complete,
			// as controlled by cancels and dones.
			var (
				cancels = make([]func(), 10)
				dones   = make([]chan struct{}, 10)
			)

			var g errgroup.Group
			var lastValue atomic.Int32 // keep track of the last successfully computed value
			for i := range 10 {
				ctx, cancel := context.WithCancel(ctx)
				done := make(chan struct{})
				cancels[i] = cancel
				dones[i] = done
				compute := compute(i, done)
				g.Go(func() error {
					v, err := c.get(ctx, 0, compute)
					if err == nil {
						lastValue.Store(int32(v))
					}
					return nil
				})
			}
			for _, cancel := range cancels[:9] {
				cancel()
			}
			defer cancels[9]()

			dones[9] <- struct{}{}
			g.Wait()

			t.Logf("started %d computations", started.Load())
			if got := lastValue.Load(); got != 9 {
				t.Errorf("after cancelling computation 0-8, got %d, want 9", got)
			}
		})
	}
}
