// Copyright 2019 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package debug

import (
	"golang.org/x/tools/internal/event/export/metric"
	"golang.org/x/tools/internal/event/label"
	"golang.org/x/tools/internal/jsonrpc2"
)

var (
	// the distributions we use for histograms
	bytesDistribution   = []int64{1 << 10, 1 << 11, 1 << 12, 1 << 14, 1 << 16, 1 << 20}
	secondsDistribution = []float64{
		0.0001, // 0.1ms
		0.0005, // 0.5ms
		0.001,  // 1ms
		0.002,  // 2ms
		0.005,  // 5ms
		0.01,   // 10ms
		0.05,   // 50ms
		0.1,    // 100ms
		0.5,    // 500ms
		1,      // 1s
		5,      // 5s
		10,     // 10s
		50,     // 50s
		100,    // 1m40s
		500,    // 8m20s
		1000,   // 16m40s
	}

	receivedBytes = metric.HistogramInt64{
		Name:        "received_bytes",
		Description: "Distribution of received bytes, by method.",
		Keys:        []label.Key{jsonrpc2.RPCDirection, jsonrpc2.Method},
		Buckets:     bytesDistribution,
	}

	sentBytes = metric.HistogramInt64{
		Name:        "sent_bytes",
		Description: "Distribution of sent bytes, by method.",
		Keys:        []label.Key{jsonrpc2.RPCDirection, jsonrpc2.Method},
		Buckets:     bytesDistribution,
	}

	latency = metric.HistogramFloat64{
		Name:        "latency",
		Description: "Distribution of latency in seconds, by method.",
		Keys:        []label.Key{jsonrpc2.RPCDirection, jsonrpc2.Method},
		Buckets:     secondsDistribution,
	}

	started = metric.Scalar{
		Name:        "started",
		Description: "Count of RPCs started by method.",
		Keys:        []label.Key{jsonrpc2.RPCDirection, jsonrpc2.Method},
	}

	completed = metric.Scalar{
		Name:        "completed",
		Description: "Count of RPCs completed by method and status.",
		Keys:        []label.Key{jsonrpc2.RPCDirection, jsonrpc2.Method, jsonrpc2.StatusCode},
	}
)

func registerMetrics(m *metric.Config) {
	receivedBytes.Record(m, jsonrpc2.ReceivedBytes)
	sentBytes.Record(m, jsonrpc2.SentBytes)
	latency.Record(m, jsonrpc2.Latency)
	started.Count(m, jsonrpc2.Started)
	completed.Count(m, jsonrpc2.Latency)
}
