// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build !go1.19
// +build !go1.19

package telemetry

// This file defines dummy implementations of telemetry operations to
// permit building with go1.18. Until we drop support for go1.18,
// gopls may not refer to the telemetry module directly, but must go
// through this file.

func CounterOpen() {}

func StartCrashMonitor() {}

func CrashMonitorSupported() bool { return false }

func NewStackCounter(string, int) dummyCounter { return dummyCounter{} }

type dummyCounter struct{}

func (dummyCounter) Inc() {}

func Mode() string {
	return "local"
}

func SetMode(mode string) error {
	return nil
}

func Upload() {
}

func RecordClientInfo(string) {}

func RecordViewGoVersion(x int) {
}

func AddForwardedCounters(names []string, values []int64) {
}
