// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package otel

import (
	"time"
)

// Default configuration values.
const (
	DefaultEndpoint    = "http://localhost:4318"
	DefaultServiceName = "unknown_service"
	DefaultTimeout     = 10 * time.Second
	DefaultFlushPeriod = 2 * time.Second
)

// Option configures an OTelExporter.
type Option func(*Exporter)

// WithEndpoint sets the OTLP HTTP endpoint.
func WithEndpoint(endpoint string) Option {
	return func(e *Exporter) {
		e.endpoint = endpoint
	}
}

// WithServiceName sets the service name for exported spans.
func WithServiceName(name string) Option {
	return func(e *Exporter) {
		e.serviceName = name
	}
}

// WithTimeout sets the HTTP client timeout.
func WithTimeout(timeout time.Duration) Option {
	return func(e *Exporter) {
		e.client.Timeout = timeout
	}
}

// WithFlushPeriod sets the interval for automatic background flushing.
func WithFlushPeriod(period time.Duration) Option {
	return func(e *Exporter) {
		e.flushPeriod = period
	}
}

// WithServiceVersion sets the service version for exported telemetry.
func WithServiceVersion(version string) Option {
	return func(e *Exporter) {
		e.serviceVersion = version
	}
}
