// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package telemetry

import "strings"

// A CounterPath represents the components of a telemetry counter name.
//
// By convention, counter names follow the format path/to/counter:bucket. The
// CounterPath holds the '/'-separated components of this path, along with a
// final element representing the bucket.
//
// CounterPaths may be used to build up counters incrementally, such as when a
// set of observed counters shared a common prefix, to be controlled by the
// caller.
type CounterPath []string

// FullName returns the counter name for the receiver.
func (p CounterPath) FullName() string {
	if len(p) == 0 {
		return ""
	}
	name := strings.Join([]string(p[:len(p)-1]), "/")
	if bucket := p[len(p)-1]; bucket != "" {
		name += ":" + bucket
	}
	return name
}
