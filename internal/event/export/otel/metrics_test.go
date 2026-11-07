// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package otel

import (
	"context"
	"testing"

	"golang.org/x/tools/internal/event"
	"golang.org/x/tools/internal/event/core"
	"golang.org/x/tools/internal/event/export/metric"
	"golang.org/x/tools/internal/event/keys"
	"golang.org/x/tools/internal/event/label"
)

// captureMetricData creates a metric using the production event system and returns the resulting data.
func captureMetricData(name string, setup func(*metric.Config), record func(context.Context)) metric.Data {
	var captured metric.Data

	output := func(ctx context.Context, ev core.Event, lm label.Map) context.Context {
		if event.IsMetric(ev) {
			if entries := metric.Entries.Get(lm); entries != nil {
				for _, data := range entries.([]metric.Data) {
					if data.Handle() == name {
						captured = data
					}
				}
			}
		}
		return ctx
	}

	cfg := metric.Config{}
	setup(&cfg)

	event.SetExporter(cfg.Exporter(output))
	defer event.SetExporter(nil)

	record(context.Background())
	return captured
}

func TestConvertInt64Data_Counter(t *testing.T) {
	key := keys.NewInt("value", "")

	data := captureMetricData("request_count", func(cfg *metric.Config) {
		metric.Scalar{
			Name:        "request_count",
			Description: "Number of requests",
		}.SumInt64(cfg, key)
	}, func(ctx context.Context) {
		event.Metric(ctx, key.Of(42))
	}).(*metric.Int64Data)

	m := convertInt64Data(data)

	if m.Name != "request_count" {
		t.Errorf("expected name 'request_count', got %q", m.Name)
	}
	if m.Description != "Number of requests" {
		t.Errorf("expected description 'Number of requests', got %q", m.Description)
	}
	if m.Gauge != nil {
		t.Error("expected Gauge to be nil for counter")
	}
	if m.Sum == nil {
		t.Fatal("expected Sum to be non-nil for counter")
	}
	if len(m.Sum.DataPoints) != 1 {
		t.Fatalf("expected 1 data point, got %d", len(m.Sum.DataPoints))
	}
	if m.Sum.DataPoints[0].AsInt == nil || *m.Sum.DataPoints[0].AsInt != 42 {
		t.Errorf("expected value 42, got %v", m.Sum.DataPoints[0].AsInt)
	}
	if !m.Sum.IsMonotonic {
		t.Error("expected IsMonotonic to be true for counter")
	}
}

func TestConvertInt64Data_Gauge(t *testing.T) {
	key := keys.NewInt("value", "")

	data := captureMetricData("current_connections", func(cfg *metric.Config) {
		metric.Scalar{Name: "current_connections"}.LatestInt64(cfg, key)
	}, func(ctx context.Context) {
		event.Metric(ctx, key.Of(100))
	}).(*metric.Int64Data)

	m := convertInt64Data(data)

	if m.Sum != nil {
		t.Error("expected Sum to be nil for gauge")
	}
	if m.Gauge == nil {
		t.Fatal("expected Gauge to be non-nil")
	}
	if len(m.Gauge.DataPoints) != 1 {
		t.Fatalf("expected 1 data point, got %d", len(m.Gauge.DataPoints))
	}
	if m.Gauge.DataPoints[0].AsInt == nil || *m.Gauge.DataPoints[0].AsInt != 100 {
		t.Errorf("expected value 100, got %v", m.Gauge.DataPoints[0].AsInt)
	}
}

func TestConvertFloat64Data(t *testing.T) {
	key := keys.NewFloat("value", "")

	data := captureMetricData("cpu_usage", func(cfg *metric.Config) {
		metric.Scalar{Name: "cpu_usage"}.LatestFloat64(cfg, key)
	}, func(ctx context.Context) {
		event.Metric(ctx, key.Of(0.75))
	}).(*metric.Float64Data)

	m := convertFloat64Data(data)

	if m.Gauge == nil {
		t.Fatal("expected Gauge to be non-nil")
	}
	if len(m.Gauge.DataPoints) != 1 {
		t.Fatalf("expected 1 data point, got %d", len(m.Gauge.DataPoints))
	}
	if m.Gauge.DataPoints[0].AsDouble == nil || *m.Gauge.DataPoints[0].AsDouble != 0.75 {
		t.Errorf("expected value 0.75, got %v", m.Gauge.DataPoints[0].AsDouble)
	}
}

func TestConvertHistogramInt64Data(t *testing.T) {
	key := keys.NewInt("value", "")
	buckets := []int64{10, 50, 100, 500}

	// Record multiple values across different buckets:
	// Bucket boundaries: <=10, <=50, <=100, <=500, >500
	// Values: 5, 8 (<=10), 20, 30, 40 (<=50), 75 (<=100), 200, 300 (<=500), 1000 (>500)
	values := []int{5, 8, 20, 30, 40, 75, 200, 300, 1000}

	data := captureMetricData("response_time", func(cfg *metric.Config) {
		metric.HistogramInt64{
			Name:        "response_time",
			Description: "Response time in ms",
			Buckets:     buckets,
		}.Record(cfg, key)
	}, func(ctx context.Context) {
		for _, v := range values {
			event.Metric(ctx, key.Of(v))
		}
	}).(*metric.HistogramInt64Data)

	m := convertHistogramInt64Data(data)

	if m.Histogram == nil {
		t.Fatal("expected Histogram to be non-nil")
	}
	dp := m.Histogram.DataPoints[0]

	// Verify count
	if dp.Count != 9 {
		t.Errorf("expected count 9, got %d", dp.Count)
	}

	// Verify sum: 5+8+20+30+40+75+200+300+1000 = 1678
	if dp.Sum == nil || *dp.Sum != 1678 {
		t.Errorf("expected sum 1678, got %v", dp.Sum)
	}

	// Verify min/max
	if dp.Min == nil || *dp.Min != 5 {
		t.Errorf("expected min 5, got %v", dp.Min)
	}
	if dp.Max == nil || *dp.Max != 1000 {
		t.Errorf("expected max 1000, got %v", dp.Max)
	}

	// Verify bucket counts (non-cumulative):
	// <=10: 2 (5, 8)
	// <=50: 3 (20, 30, 40)
	// <=100: 1 (75)
	// <=500: 2 (200, 300)
	// >500: 1 (1000)
	expectedBuckets := []intAsString{2, 3, 1, 2, 1}
	if len(dp.BucketCounts) != len(expectedBuckets) {
		t.Fatalf("expected %d buckets, got %d", len(expectedBuckets), len(dp.BucketCounts))
	}
	for i, expected := range expectedBuckets {
		if dp.BucketCounts[i] != expected {
			t.Errorf("bucket[%d]: expected %d, got %d", i, expected, dp.BucketCounts[i])
		}
	}

	// Verify explicit bounds match configured buckets
	expectedBounds := []float64{10, 50, 100, 500}
	if len(dp.ExplicitBounds) != len(expectedBounds) {
		t.Fatalf("expected %d bounds, got %d", len(expectedBounds), len(dp.ExplicitBounds))
	}
	for i, expected := range expectedBounds {
		if dp.ExplicitBounds[i] != expected {
			t.Errorf("bound[%d]: expected %v, got %v", i, expected, dp.ExplicitBounds[i])
		}
	}
}

func TestConvertInt64Data_WithAttributes(t *testing.T) {
	valueKey := keys.NewInt("value", "")
	methodKey := keys.NewString("method", "")

	data := captureMetricData("requests_by_method", func(cfg *metric.Config) {
		metric.Scalar{
			Name: "requests_by_method",
			Keys: []label.Key{methodKey}, // Group by method
		}.SumInt64(cfg, valueKey)
	}, func(ctx context.Context) {
		event.Metric(ctx, valueKey.Of(10), methodKey.Of("GET"))
		event.Metric(ctx, valueKey.Of(5), methodKey.Of("POST"))
	}).(*metric.Int64Data)

	m := convertInt64Data(data)

	// Should have 2 data points (one per method)
	if len(m.Sum.DataPoints) != 2 {
		t.Fatalf("expected 2 data points, got %d", len(m.Sum.DataPoints))
	}

	// Verify attributes contain the method labels
	for _, dp := range m.Sum.DataPoints {
		if len(dp.Attributes) != 1 {
			t.Errorf("expected 1 attribute, got %d", len(dp.Attributes))
		}
		if dp.Attributes[0].Key != "method" {
			t.Errorf("expected attribute key 'method', got %q", dp.Attributes[0].Key)
		}
		// Value should be "GET" or "POST"
		sv := dp.Attributes[0].Value.StringValue
		if sv == nil || (*sv != "GET" && *sv != "POST") {
			t.Errorf("unexpected method value: %v", sv)
		}
	}
}
