// Copyright 2019 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ocagent

import (
	"fmt"

	"golang.org/x/tools/internal/telemetry"
	"golang.org/x/tools/internal/telemetry/export/ocagent/wire"
	"golang.org/x/tools/internal/telemetry/metric"
)

// dataToMetricDescriptor return a *wire.MetricDescriptor based on data.
func dataToMetricDescriptor(data telemetry.MetricData) *wire.MetricDescriptor {
	if data == nil {
		return nil
	}
	descriptor := &wire.MetricDescriptor{
		Name:        data.Handle(),
		Description: getDescription(data),
		// TODO: Unit?
		Type:      dataToMetricDescriptorType(data),
		LabelKeys: getLabelKeys(data),
	}

	return descriptor
}

// getDescription returns the description of data.
func getDescription(data telemetry.MetricData) string {
	switch d := data.(type) {
	case *metric.Int64Data:
		return d.Info.Description

	case *metric.Float64Data:
		return d.Info.Description
	}

	return ""
}

// getLabelKeys returns an array of *wire.LabelKeys based on the keys
// in data.
func getLabelKeys(data telemetry.MetricData) []*wire.LabelKey {
	switch d := data.(type) {
	case *metric.Int64Data:
		return infoKeysToLabelKeys(d.Info.Keys)

	case *metric.Float64Data:
		return infoKeysToLabelKeys(d.Info.Keys)
	}

	return nil
}

// dataToMetricDescriptorType returns a wire.MetricDescriptor_Type based on the
// underlying type of data.
func dataToMetricDescriptorType(data telemetry.MetricData) wire.MetricDescriptor_Type {
	if data == nil {
		return wire.MetricDescriptor_UNSPECIFIED
	}

	switch d := data.(type) {
	case *metric.Int64Data:
		if d.IsGauge {
			return wire.MetricDescriptor_GAUGE_INT64
		}
		return wire.MetricDescriptor_CUMULATIVE_INT64

	case *metric.Float64Data:
		if d.IsGauge {
			return wire.MetricDescriptor_GAUGE_DOUBLE
		}
		return wire.MetricDescriptor_CUMULATIVE_DOUBLE
	}

	return wire.MetricDescriptor_UNSPECIFIED
}

// dataToTimeseries returns an array of *wire.TimeSeries based on the
// points in data.
func dataToTimeseries(data telemetry.MetricData) []*wire.TimeSeries {
	if data == nil {
		return nil
	}

	timeseries := make([]*wire.TimeSeries, 0, len(data.Groups()))

	for groups, i := data.Groups(), 0; i < len(groups); i++ {
		group := groups[i]
		points := dataToPoints(data, i)
		timeseries = append(timeseries, &wire.TimeSeries{
			// TODO: attach StartTimestamp
			LabelValues: tagsToLabelValues(group),
			Points:      points,
		})
	}

	return timeseries
}

// dataToPoints returns an array of *wire.Points based on the point(s)
// in data at index i.
func dataToPoints(data telemetry.MetricData, i int) []*wire.Point {
	switch d := data.(type) {
	case *metric.Int64Data:
		return []*wire.Point{
			{
				Value: wire.PointInt64Value{
					Int64Value: d.Rows[i],
				},
				// TODO: attach Timestamp
			},
		}
	case *metric.Float64Data:
		return []*wire.Point{
			{
				Value: wire.PointDoubleValue{
					DoubleValue: d.Rows[i],
				},
				// TODO: attach Timestamp
			},
		}
	}

	return nil
}

// infoKeysToLabelKeys returns an array of *wire.LabelKeys containing the
// string values of the elements of labelKeys.
func infoKeysToLabelKeys(infoKeys []interface{}) []*wire.LabelKey {
	labelKeys := make([]*wire.LabelKey, 0, len(infoKeys))
	for _, key := range infoKeys {
		labelKeys = append(labelKeys, &wire.LabelKey{
			Key: fmt.Sprintf("%v", key),
		})
	}

	return labelKeys
}

//tagsToLabelValues returns an array of *wire.LabelValues containing the
// string values of the elements of tagKeys.
func tagsToLabelValues(tags []telemetry.Tag) []*wire.LabelValue {
	labelValues := make([]*wire.LabelValue, 0, len(tags))
	for _, tagKey := range tags {
		labelValues = append(labelValues, &wire.LabelValue{
			Value:    fmt.Sprintf("%v", tagKey.Value),
			HasValue: true,
		})
	}

	return labelValues
}
