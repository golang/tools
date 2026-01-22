// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package otel

import (
	"slices"

	"golang.org/x/tools/internal/event"
	"golang.org/x/tools/internal/event/core"
	"golang.org/x/tools/internal/event/export"
	"golang.org/x/tools/internal/event/keys"
)

// exportTraces sends traces to the OTLP endpoint.
func (e *Exporter) exportTraces(spans []otlpSpan) error {
	req := otlpTraceExportRequest{
		ResourceSpans: []otlpResourceSpans{{
			Resource: e.resource,
			ScopeSpans: []otlpScopeSpans{{
				Scope: otlpScope{Name: "golang.org/x/tools"},
				Spans: spans,
			}},
		}},
	}
	return e.post("/v1/traces", req)
}

// convertSpan converts an internal Span to an OTLP span.
func convertSpan(span *export.Span) otlpSpan {
	s := otlpSpan{
		TraceID: span.ID.TraceID.String(),
		SpanID:  span.ID.SpanID.String(),
		Name:    span.Name,
		Kind:    1,                       // SPAN_KIND_INTERNAL
		Status:  otlpSpanStatus{Code: 0}, // STATUS_CODE_UNSET
	}

	if span.ParentID.IsValid() {
		s.ParentSpanID = span.ParentID.String()
	}

	// Get timestamps from start event
	startEvent := span.Start()
	if !startEvent.At().IsZero() {
		s.StartTimeUnixNano = intAsString(startEvent.At().UnixNano())
	}

	// Get end timestamp from finish event
	finishEvent := span.Finish()
	if !finishEvent.At().IsZero() {
		s.EndTimeUnixNano = intAsString(finishEvent.At().UnixNano())
	}

	// Check for errors in span events to set status
	if slices.ContainsFunc(span.Events(), event.IsError) {
		s.Status.Code = 2 // STATUS_CODE_ERROR
	}

	// Extract attributes from span events
	s.Attributes = extractAttributes(span)

	return s
}

// extractAttributes extracts labels from span events as OTLP attributes.
func extractAttributes(span *export.Span) []otlpAttribute {
	var attrs []otlpAttribute

	// Extract from start event
	startEvent := span.Start()
	attrs = appendEventAttributes(attrs, startEvent)

	// Extract from logged events
	for _, ev := range span.Events() {
		attrs = appendEventAttributes(attrs, ev)
	}

	return attrs
}

// appendEventAttributes appends labels from an event as attributes.
func appendEventAttributes(attrs []otlpAttribute, ev core.Event) []otlpAttribute {
	for l := range ev.Labels() {
		key := l.Key()
		// Skip internal event type markers
		if key == keys.Start || key == keys.End || key == keys.Label ||
			key == keys.Detach || key == keys.Metric {
			continue
		}

		if attr, ok := labelToAttribute(l); ok {
			attrs = append(attrs, attr)
		}
	}
	return attrs
}
