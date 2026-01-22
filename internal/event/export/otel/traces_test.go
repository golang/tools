// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package otel

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"golang.org/x/tools/internal/event"
	"golang.org/x/tools/internal/event/core"
	"golang.org/x/tools/internal/event/export"
	"golang.org/x/tools/internal/event/label"
)

func TestConvertSpan(t *testing.T) {
	span := &export.Span{
		Name: "test-operation",
		ID: export.SpanContext{
			TraceID: export.TraceID{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16},
			SpanID:  export.SpanID{1, 2, 3, 4, 5, 6, 7, 8},
		},
		ParentID: export.SpanID{8, 7, 6, 5, 4, 3, 2, 1},
	}

	result := convertSpan(span)

	if result.Name != "test-operation" {
		t.Errorf("expected name 'test-operation', got %q", result.Name)
	}
	if result.TraceID != "0102030405060708090a0b0c0d0e0f10" {
		t.Errorf("unexpected traceId: %s", result.TraceID)
	}
	if result.SpanID != "0102030405060708" {
		t.Errorf("unexpected spanId: %s", result.SpanID)
	}
	if result.ParentSpanID != "0807060504030201" {
		t.Errorf("unexpected parentSpanId: %s", result.ParentSpanID)
	}
	if result.Kind != 1 {
		t.Errorf("expected kind 1, got %d", result.Kind)
	}
	if result.Status.Code != 0 {
		t.Errorf("expected status code 0, got %d", result.Status.Code)
	}
}

// TestE2E_SpanExport is a minimal end-to-end test verifying the full pipeline.
func TestE2E_SpanExport(t *testing.T) {
	var received []byte
	var mu sync.Mutex

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		received, _ = io.ReadAll(r.Body)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	baseExporter := func(ctx context.Context, ev core.Event, lm label.Map) context.Context {
		return ctx
	}
	spansExporter := export.Spans(baseExporter)
	otelExporter := NewExporter(context.Background(), WithEndpoint(server.URL))

	chainedExporter := func(ctx context.Context, ev core.Event, lm label.Map) context.Context {
		ctx = spansExporter(ctx, ev, lm)
		return otelExporter.ProcessEvent(ctx, ev, lm)
	}

	event.SetExporter(chainedExporter)

	ctx := context.Background()
	ctx, end := event.Start(ctx, "e2e-span")
	end()

	otelExporter.Flush()

	mu.Lock()
	data := received
	mu.Unlock()

	if len(data) == 0 {
		t.Fatal("no data received")
	}

	// Just verify it's valid JSON with a span
	var req otlpTraceExportRequest
	if err := json.Unmarshal(data, &req); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if len(req.ResourceSpans) == 0 ||
		len(req.ResourceSpans[0].ScopeSpans) == 0 ||
		len(req.ResourceSpans[0].ScopeSpans[0].Spans) == 0 {
		t.Fatal("expected at least one span")
	}
}
