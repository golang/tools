package ocagent

import (
	"reflect"
	"testing"

	"golang.org/x/tools/internal/telemetry"
	"golang.org/x/tools/internal/telemetry/export/ocagent/wire"
	"golang.org/x/tools/internal/telemetry/metric"
)

func TestDataToMetricDescriptor(t *testing.T) {
	intData := &metric.Int64Data{
		Info: &metric.Scalar{
			Name:        "int",
			Description: "int metric",
			Keys:        []interface{}{"hello"},
		},
		IsGauge: true,
	}
	floatData := &metric.Float64Data{
		Info: &metric.Scalar{
			Name:        "float",
			Description: "float metric",
			Keys:        []interface{}{"world"},
		},
		IsGauge: false,
	}

	tests := []struct {
		name string
		data telemetry.MetricData
		want *wire.MetricDescriptor
	}{
		{
			"nil data",
			nil,
			nil,
		},
		{
			"Int64Data gauge",
			intData,
			&wire.MetricDescriptor{
				Name:        "int",
				Description: "int metric",
				Type:        wire.MetricDescriptor_GAUGE_INT64,
				LabelKeys: []*wire.LabelKey{
					&wire.LabelKey{
						Key: "hello",
					},
				},
			},
		},
		{
			"Float64Data cumulative",
			floatData,
			&wire.MetricDescriptor{
				Name:        "float",
				Description: "float metric",
				Type:        wire.MetricDescriptor_CUMULATIVE_DOUBLE,
				LabelKeys: []*wire.LabelKey{
					&wire.LabelKey{
						Key: "world",
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := dataToMetricDescriptor(tt.data)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("Got:\n%s\nWant:\n%s", marshaled(got), marshaled(tt.want))
			}
		})
	}
}

func TestGetDescription(t *testing.T) {
	intData := &metric.Int64Data{
		Info: &metric.Scalar{
			Description: "int metric",
		},
	}
	floatData := &metric.Float64Data{
		Info: &metric.Scalar{
			Description: "float metric",
		},
	}
	tests := []struct {
		name string
		data telemetry.MetricData
		want string
	}{
		{
			"nil data",
			nil,
			"",
		},
		{
			"Int64Data description",
			intData,
			"int metric",
		},
		{
			"Float64Data description",
			floatData,
			"float metric",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := getDescription(tt.data)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("Got:\n%s\nWant:\n%s", marshaled(got), marshaled(tt.want))
			}
		})
	}

}

func TestGetLabelKeys(t *testing.T) {
	intData := &metric.Int64Data{
		Info: &metric.Scalar{
			Keys: []interface{}{
				"hello",
			},
		},
	}
	floatData := &metric.Float64Data{
		Info: &metric.Scalar{
			Keys: []interface{}{
				"world",
			},
		},
	}

	tests := []struct {
		name string
		data telemetry.MetricData
		want []*wire.LabelKey
	}{
		{
			"nil label keys",
			nil,
			nil,
		},
		{
			"Int64Data label keys",
			intData,
			[]*wire.LabelKey{
				&wire.LabelKey{
					Key: "hello",
				},
			},
		},
		{
			"Float64Data label keys",
			floatData,
			[]*wire.LabelKey{
				&wire.LabelKey{
					Key: "world",
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := getLabelKeys(tt.data)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("Got:\n%s\nWant:\n%s", marshaled(got), marshaled(tt.want))
			}
		})
	}
}

func TestDataToMetricDescriptorType(t *testing.T) {
	tests := []struct {
		name string
		data telemetry.MetricData
		want wire.MetricDescriptor_Type
	}{
		{
			"Nil data",
			nil,
			wire.MetricDescriptor_UNSPECIFIED,
		},
		{
			"Gauge Int64",
			&metric.Int64Data{
				IsGauge: true,
			},
			wire.MetricDescriptor_GAUGE_INT64,
		},
		{
			"Cumulative Int64",
			&metric.Int64Data{
				IsGauge: false,
			},
			wire.MetricDescriptor_CUMULATIVE_INT64,
		},
		{
			"Gauge Float64",
			&metric.Float64Data{
				IsGauge: true,
			},
			wire.MetricDescriptor_GAUGE_DOUBLE,
		},
		{
			"Cumulative Float64",
			&metric.Float64Data{
				IsGauge: false,
			},
			wire.MetricDescriptor_CUMULATIVE_DOUBLE,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := dataToMetricDescriptorType(tt.data)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("Got:\n%s\nWant:\n%s", marshaled(got), marshaled(tt.want))
			}
		})
	}
}

func TestDataToTimeseries(t *testing.T) {
	tests := []struct {
		name string
		data telemetry.MetricData
		want []*wire.TimeSeries
	}{
		{
			"nil data",
			nil,
			nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := dataToTimeseries(tt.data)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("Got:\n%s\nWant:\n%s", marshaled(got), marshaled(tt.want))
			}
		})
	}
}

func TestDataToPoints(t *testing.T) {

	int64Data := &metric.Int64Data{
		Rows: []int64{
			0,
			10,
		},
	}

	float64Data := &metric.Float64Data{
		Rows: []float64{
			0.5,
			0.25,
		},
	}

	tests := []struct {
		name string
		data telemetry.MetricData
		i    int
		want []*wire.Point
	}{
		{
			"nil data",
			nil,
			0,
			nil,
		},
		{
			"Int64data index 0",
			int64Data,
			0,
			[]*wire.Point{
				{
					Value: wire.PointInt64Value{
						Int64Value: 0,
					},
				},
			},
		},
		{
			"Int64data index 1",
			int64Data,
			1,
			[]*wire.Point{
				{
					Value: wire.PointInt64Value{
						Int64Value: 10,
					},
				},
			},
		},
		{
			"Float64Data index 0",
			float64Data,
			0,
			[]*wire.Point{
				{
					Value: wire.PointDoubleValue{
						DoubleValue: 0.5,
					},
				},
			},
		},
		{
			"Float64Data index 1",
			float64Data,
			1,
			[]*wire.Point{
				{
					Value: wire.PointDoubleValue{
						DoubleValue: 0.25,
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := dataToPoints(tt.data, tt.i)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("Got:\n%s\nWant:\n%s", marshaled(got), marshaled(tt.want))
			}
		})
	}

}

func TestInfoKeysToLabelKeys(t *testing.T) {
	tests := []struct {
		name     string
		infoKeys []interface{}
		want     []*wire.LabelKey
	}{
		{
			"empty infoKeys",
			[]interface{}{},
			[]*wire.LabelKey{},
		},
		{
			"empty string infoKey",
			[]interface{}{""},
			[]*wire.LabelKey{
				&wire.LabelKey{
					Key: "",
				},
			},
		},
		{
			"non-empty string infoKey",
			[]interface{}{"hello"},
			[]*wire.LabelKey{
				&wire.LabelKey{
					Key: "hello",
				},
			},
		},
		{
			"multiple element infoKey",
			[]interface{}{"hello", "world"},
			[]*wire.LabelKey{
				&wire.LabelKey{
					Key: "hello",
				},
				&wire.LabelKey{
					Key: "world",
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := infoKeysToLabelKeys(tt.infoKeys)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("Got:\n%s\nWant:\n%s", marshaled(got), marshaled(tt.want))
			}
		})
	}
}

func TestTagsToLabelValues(t *testing.T) {
	tests := []struct {
		name string
		tags []telemetry.Tag
		want []*wire.LabelValue
	}{
		{
			"empty tagKeys",
			[]telemetry.Tag{},
			[]*wire.LabelValue{},
		},
		{
			"empty string tagKey",
			[]telemetry.Tag{
				telemetry.Tag{Value: ""},
			},
			[]*wire.LabelValue{
				&wire.LabelValue{
					Value:    "",
					HasValue: true,
				},
			},
		},
		{
			"non-empty string tagKey",
			[]telemetry.Tag{
				telemetry.Tag{Value: "hello"},
			},
			[]*wire.LabelValue{
				&wire.LabelValue{
					Value:    "hello",
					HasValue: true,
				},
			},
		},
		{
			"multiple element tagKey",
			[]telemetry.Tag{
				telemetry.Tag{Value: "hello"},
				telemetry.Tag{Value: "world"},
			},
			[]*wire.LabelValue{
				&wire.LabelValue{
					Value:    "hello",
					HasValue: true,
				},
				&wire.LabelValue{
					Value:    "world",
					HasValue: true,
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tagsToLabelValues(tt.tags)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("Got:\n%s\nWant:\n%s", marshaled(got), marshaled(tt.want))
			}
		})
	}
}
