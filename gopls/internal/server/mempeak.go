// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package server

import (
	"runtime"
	"sync"
	"sync/atomic"
	"time"
)

// This file implements a lightweight in-process high-water tracker for heap
// memory, used by the MemStats command to measure the peak memory of a discrete
// operation (e.g. a re-diagnosis pass) reliably, regardless of how briefly the
// peak occurs. External sampling (e.g. polling /debug/pprof/heap) cannot
// reliably catch the peak of a fast operation.
//
// The tracker samples runtime.MemStats on a fixed interval and records the
// maximum HeapInuse and Sys observed. The MemStats command reads-and-resets the
// maxima, so a caller can bracket an operation with two MemStats calls and learn
// its peak heap footprint.

var (
	memPeakOnce      sync.Once
	memPeakHeapInuse atomic.Uint64
	memPeakSys       atomic.Uint64
)

// startMemPeakTracker starts the background sampler (idempotent).
func startMemPeakTracker() {
	memPeakOnce.Do(func() {
		go func() {
			var m runtime.MemStats
			t := time.NewTicker(30 * time.Millisecond)
			defer t.Stop()
			for range t.C {
				runtime.ReadMemStats(&m)
				atomicMax(&memPeakHeapInuse, m.HeapInuse)
				atomicMax(&memPeakSys, m.Sys)
			}
		}()
	})
}

// readAndResetMemPeak returns the maximum HeapInuse and Sys observed since the
// last call, and resets the maxima to the provided current values.
func readAndResetMemPeak(curHeapInuse, curSys uint64) (peakHeapInuse, peakSys uint64) {
	return memPeakHeapInuse.Swap(curHeapInuse), memPeakSys.Swap(curSys)
}

func atomicMax(a *atomic.Uint64, v uint64) {
	for {
		cur := a.Load()
		if v <= cur || a.CompareAndSwap(cur, v) {
			return
		}
	}
}
