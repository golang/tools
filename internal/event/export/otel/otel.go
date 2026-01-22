// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package otel exports spans and metrics to an OpenTelemetry
// collector using the OTLP HTTP/JSON protocol.
//
// Use [NewExporter] to create an exporter, then chain its
// [Exporter.ProcessEvent] method with other exporters:
//
//	otelExporter := otel.NewExporter(ctx,
//		otel.WithEndpoint("http://localhost:4318"),
//		otel.WithServiceName("myservice"),
//	)
//	event.SetExporter(otelExporter.ProcessEvent)
//
// The exporter batches telemetry and flushes periodically in a
// background goroutine. Call [Exporter.Flush] to force an
// immediate export.
package otel

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"sync"
	"time"

	"golang.org/x/tools/internal/event"
	"golang.org/x/tools/internal/event/core"
	"golang.org/x/tools/internal/event/export"
	"golang.org/x/tools/internal/event/export/metric"
	"golang.org/x/tools/internal/event/label"
)

// Exporter exports spans and metrics to an OTLP HTTP endpoint.
type Exporter struct {
	mu             sync.Mutex
	endpoint       string
	serviceName    string
	serviceVersion string
	resource       otlpResource
	client         *http.Client
	flushPeriod    time.Duration
	spans          []*export.Span
	metrics        map[string]metric.Data
}

// NewExporter creates an exporter that sends spans to an OTLP endpoint.
// Spans are collected and exported periodically in a background goroutine.
// When the context is done, remaining spans are flushed.
func NewExporter(ctx context.Context, opts ...Option) *Exporter {
	e := &Exporter{
		endpoint:    DefaultEndpoint,
		serviceName: DefaultServiceName,
		client:      &http.Client{Timeout: DefaultTimeout},
		flushPeriod: DefaultFlushPeriod,
	}

	for _, opt := range opts {
		opt(e)
	}

	e.resource = e.buildResource()

	go func() {
		ticker := time.NewTicker(e.flushPeriod)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				e.Flush() // final flush before exiting
				return
			case <-ticker.C:
				e.Flush()
			}
		}
	}()

	return e
}

// ProcessEvent handles events and collects completed spans and metrics.
func (e *Exporter) ProcessEvent(ctx context.Context, ev core.Event, lm label.Map) context.Context {
	e.mu.Lock()
	defer e.mu.Unlock()

	if event.IsEnd(ev) {
		if span := export.GetSpan(ctx); span != nil {
			e.spans = append(e.spans, span)
		}
	}

	if event.IsMetric(ev) {
		if e.metrics == nil {
			e.metrics = make(map[string]metric.Data)
		}
		if entries := metric.Entries.Get(lm); entries != nil {
			for _, data := range entries.([]metric.Data) {
				e.metrics[data.Handle()] = data
			}
		}
	}

	return ctx
}

// Flush exports all collected spans and metrics to the OTLP endpoint.
func (e *Exporter) Flush() {
	e.mu.Lock()
	spans := e.spans
	e.spans = nil
	metrics := e.metrics
	e.metrics = nil
	e.mu.Unlock()

	// Export spans
	if len(spans) > 0 {
		otlpSpans := make([]otlpSpan, 0, len(spans))
		for _, span := range spans {
			otlpSpans = append(otlpSpans, convertSpan(span))
		}
		if err := e.exportTraces(otlpSpans); err != nil {
			log.Printf("post spans: %v", err)
			return
		}
	}

	// Export metrics
	if len(metrics) > 0 {
		otlpMetrics := make([]otlpMetric, 0, len(metrics))
		for _, data := range metrics {
			otlpMetrics = append(otlpMetrics, convertMetric(data))
		}
		if err := e.exportMetrics(otlpMetrics); err != nil {
			log.Printf("post metrics: %v", err)
		}
	}
}

// post sends a JSON payload to the OTLP endpoint.
func (e *Exporter) post(path string, payload any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	// The background context is appropriate here since post is called only
	// from Flush, which is called only by the Exporter's background goroutine.
	// Notably it may be called after cancellation of the client's context.
	req, err := http.NewRequestWithContext(context.Background(), "POST", e.endpoint+path, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := e.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("OTLP export to %s failed with status %d", path, resp.StatusCode)
	}
	return nil
}

func (e *Exporter) buildResource() otlpResource {
	attrs := []otlpAttribute{
		{Key: "service.name", Value: otlpAttributeValue{StringValue: &e.serviceName}},
		{Key: "process.runtime.version", Value: otlpAttributeValue{StringValue: strPtr(runtime.Version())}},
		{Key: "process.pid", Value: otlpAttributeValue{StringValue: strPtr(strconv.Itoa(os.Getpid()))}},
		{Key: "host.arch", Value: otlpAttributeValue{StringValue: strPtr(runtime.GOARCH)}},
		{Key: "os.type", Value: otlpAttributeValue{StringValue: strPtr(runtime.GOOS)}},
	}

	if hostname, err := os.Hostname(); err == nil {
		attrs = append(attrs, otlpAttribute{
			Key:   "host.name",
			Value: otlpAttributeValue{StringValue: &hostname},
		})
	}

	if e.serviceVersion != "" {
		attrs = append(attrs, otlpAttribute{
			Key:   "service.version",
			Value: otlpAttributeValue{StringValue: &e.serviceVersion},
		})
	}

	return otlpResource{Attributes: attrs}
}
