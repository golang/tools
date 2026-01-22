// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package otel

import (
	"golang.org/x/tools/internal/event/export/metric"
	"golang.org/x/tools/internal/event/label"
)

// convertMetric converts internal metric.Data to an OTLP metric.
func convertMetric(data metric.Data) otlpMetric {
	switch data := data.(type) {
	case *metric.Int64Data:
		return convertInt64Data(data)
	case *metric.Float64Data:
		return convertFloat64Data(data)
	case *metric.HistogramInt64Data:
		return convertHistogramInt64Data(data)
	case *metric.HistogramFloat64Data:
		return convertHistogramFloat64Data(data)
	default:
		return otlpMetric{Name: data.Handle()}
	}
}

func convertInt64Data(d *metric.Int64Data) otlpMetric {
	m := otlpMetric{
		Name:        d.Info.Name,
		Description: d.Info.Description,
	}

	groups := d.Groups()
	dataPoints := make([]otlpNumberDataPoint, len(d.Rows))
	for i, value := range d.Rows {
		v := intAsString(value)
		dp := otlpNumberDataPoint{
			TimeUnixNano: intAsString(d.EndTime.UnixNano()),
			AsInt:        &v, // TODO: use go1.26 new(value)
			Attributes:   labelsToAttributes(groups[i]),
		}
		dataPoints[i] = dp
	}

	if d.IsGauge {
		m.Gauge = &otlpGauge{DataPoints: dataPoints}
	} else {
		m.Sum = &otlpSum{
			DataPoints:             dataPoints,
			AggregationTemporality: 2, // cumulative
			IsMonotonic:            true,
		}
	}

	return m
}

func convertFloat64Data(d *metric.Float64Data) otlpMetric {
	m := otlpMetric{
		Name:        d.Info.Name,
		Description: d.Info.Description,
	}

	groups := d.Groups()
	dataPoints := make([]otlpNumberDataPoint, len(d.Rows))
	for i, value := range d.Rows {
		v := value
		dp := otlpNumberDataPoint{
			TimeUnixNano: intAsString(d.EndTime.UnixNano()),
			AsDouble:     &v, // TODO: use go1.26 new(value)
			Attributes:   labelsToAttributes(groups[i]),
		}
		dataPoints[i] = dp
	}

	if d.IsGauge {
		m.Gauge = &otlpGauge{DataPoints: dataPoints}
	} else {
		m.Sum = &otlpSum{
			DataPoints:             dataPoints,
			AggregationTemporality: 2, // cumulative
			IsMonotonic:            true,
		}
	}

	return m
}

func convertHistogramInt64Data(d *metric.HistogramInt64Data) otlpMetric {
	dataPoints := make([]otlpHistogramDataPoint, len(d.Rows))

	// Convert bucket boundaries to float64
	bounds := make([]float64, len(d.Info.Buckets))
	for i, b := range d.Info.Buckets {
		bounds[i] = float64(b)
	}

	groups := d.Groups()
	for i, row := range d.Rows {
		// OTLP expects bucket counts, not cumulative counts
		// The internal format uses cumulative, so we need to convert
		bucketCounts := make([]intAsString, len(row.Values)+1)
		var prev int64
		for j, v := range row.Values {
			bucketCounts[j] = intAsString(v - prev)
			prev = v
		}
		// Last bucket is for values > last bound
		bucketCounts[len(row.Values)] = intAsString(row.Count - prev)

		sum := float64(row.Sum)
		min := float64(row.Min)
		max := float64(row.Max)
		dp := otlpHistogramDataPoint{
			TimeUnixNano:   intAsString(d.EndTime.UnixNano()),
			Count:          intAsString(row.Count),
			Sum:            &sum, // TODO: use go1.26 new(value)
			BucketCounts:   bucketCounts,
			ExplicitBounds: bounds,
			Min:            &min, // TODO: use go1.26 new(value)
			Max:            &max, // TODO: use go1.26 new(value)
			Attributes:     labelsToAttributes(groups[i]),
		}
		dataPoints[i] = dp
	}

	return otlpMetric{
		Name:        d.Info.Name,
		Description: d.Info.Description,
		Histogram: &otlpHistogram{
			DataPoints:             dataPoints,
			AggregationTemporality: 2, // cumulative
		},
	}
}

func convertHistogramFloat64Data(d *metric.HistogramFloat64Data) otlpMetric {
	dataPoints := make([]otlpHistogramDataPoint, len(d.Rows))

	groups := d.Groups()
	for i, row := range d.Rows {
		// Convert cumulative to bucket counts
		bucketCounts := make([]intAsString, len(row.Values)+1)
		var prev int64
		for j, v := range row.Values {
			bucketCounts[j] = intAsString(v - prev)
			prev = v
		}
		bucketCounts[len(row.Values)] = intAsString(row.Count - prev)

		sum := row.Sum
		min := row.Min
		max := row.Max
		dp := otlpHistogramDataPoint{
			TimeUnixNano:   intAsString(d.EndTime.UnixNano()),
			Count:          intAsString(row.Count),
			Sum:            &sum, // TODO: use go1.26 new(value)
			BucketCounts:   bucketCounts,
			ExplicitBounds: d.Info.Buckets,
			Min:            &min, // TODO: use go1.26 new(value)
			Max:            &max, // TODO: use go1.26 new(value)
			Attributes:     labelsToAttributes(groups[i]),
		}
		dataPoints[i] = dp
	}

	return otlpMetric{
		Name:        d.Info.Name,
		Description: d.Info.Description,
		Histogram: &otlpHistogram{
			DataPoints:             dataPoints,
			AggregationTemporality: 2, // cumulative
		},
	}
}

// labelsToAttributes converts a slice of labels to OTLP attributes.
func labelsToAttributes(labels []label.Label) []otlpAttribute {
	attrs := make([]otlpAttribute, 0, len(labels))
	for _, l := range labels {
		if attr, ok := labelToAttribute(l); ok {
			attrs = append(attrs, attr)
		}
	}
	return attrs
}

// exportMetrics sends metrics to the OTLP endpoint.
func (e *Exporter) exportMetrics(metrics []otlpMetric) error {
	req := otlpMetricsRequest{
		ResourceMetrics: []otlpResourceMetrics{{
			Resource: e.resource,
			ScopeMetrics: []otlpScopeMetrics{{
				Scope:   otlpScope{Name: "golang.org/x/tools"},
				Metrics: metrics,
			}},
		}},
	}
	return e.post("/v1/metrics", req)
}
