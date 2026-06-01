package parquet

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.k6.io/k6/metrics"
)

func TestSampleToRow_PromotedTagsAndSpill(t *testing.T) {
	t.Parallel()

	registry := metrics.NewRegistry()
	m, err := registry.NewMetric("http_req_duration", metrics.Trend, metrics.Time)
	require.NoError(t, err)

	tags := registry.RootTagSet().
		With("scenario", "checkout").
		With("method", "POST").
		With("status", "503").
		With("error_code", "1501").
		With("expected_response", "false").
		With("ip", "10.0.0.1").
		With("team", "payments") // not promoted

	now := time.Date(2026, 5, 11, 12, 0, 0, 0, time.UTC)
	row := sampleToRow(metrics.Sample{
		TimeSeries: metrics.TimeSeries{Metric: m, Tags: tags},
		Time:       now,
		Value:      123.4,
		Metadata:   map[string]string{"vu": "42", "iter": "9", "trace_id": "abc-123"},
	})

	assert.Equal(t, now.UnixNano(), row.TsUnixNano)
	assert.Equal(t, "http_req_duration", row.MetricName)
	assert.Equal(t, "trend", row.MetricType)
	assert.Equal(t, "time", row.ValueType)
	assert.Equal(t, 123.4, row.Value)

	require.NotNil(t, row.Scenario)
	assert.Equal(t, "checkout", *row.Scenario)
	require.NotNil(t, row.Method)
	assert.Equal(t, "POST", *row.Method)
	require.NotNil(t, row.Status)
	assert.EqualValues(t, 503, *row.Status)
	require.NotNil(t, row.ErrorCode)
	assert.EqualValues(t, 1501, *row.ErrorCode)
	require.NotNil(t, row.ExpectedResponse)
	assert.False(t, *row.ExpectedResponse)
	require.NotNil(t, row.IP)
	assert.Equal(t, "10.0.0.1", *row.IP)

	require.NotNil(t, row.VU)
	assert.EqualValues(t, 42, *row.VU)
	require.NotNil(t, row.Iter)
	assert.EqualValues(t, 9, *row.Iter)

	assert.Equal(t, map[string]string{"team": "payments"}, row.ExtraTags)
	assert.Equal(t, map[string]string{"trace_id": "abc-123"}, row.ExtraMetadata)
}

func TestSampleToRow_SubMetric(t *testing.T) {
	t.Parallel()

	registry := metrics.NewRegistry()
	parent, err := registry.NewMetric("http_req_duration", metrics.Trend, metrics.Time)
	require.NoError(t, err)
	sub, err := parent.AddSubmetric("status:200")
	require.NoError(t, err)

	row := sampleToRow(metrics.Sample{
		TimeSeries: metrics.TimeSeries{Metric: sub.Metric, Tags: registry.RootTagSet()},
		Time:       time.Now(),
		Value:      10,
	})

	require.NotNil(t, row.SubmetricParent)
	assert.Equal(t, "http_req_duration", *row.SubmetricParent)
	require.NotNil(t, row.SubmetricSuffix)
	assert.Equal(t, "status:200", *row.SubmetricSuffix)
}

func TestSampleToRow_BadNumericTagFallsThrough(t *testing.T) {
	t.Parallel()

	registry := metrics.NewRegistry()
	m, err := registry.NewMetric("http_req_failed", metrics.Rate)
	require.NoError(t, err)

	// status should be int-parsed, but if it isn't we want the raw value
	// preserved in ExtraTags rather than silently dropped.
	tags := registry.RootTagSet().With("status", "abrupt-disconnect")

	row := sampleToRow(metrics.Sample{
		TimeSeries: metrics.TimeSeries{Metric: m, Tags: tags},
		Time:       time.Now(),
		Value:      1,
	})

	assert.Nil(t, row.Status)
	assert.Equal(t, "abrupt-disconnect", row.ExtraTags["status"])
}

func TestSampleToRow_BadBoolTagFallsThrough(t *testing.T) {
	t.Parallel()

	registry := metrics.NewRegistry()
	m, err := registry.NewMetric("http_req_failed", metrics.Rate)
	require.NoError(t, err)

	// expected_response must be parsed as a bool; anything strconv.ParseBool
	// rejects should land in ExtraTags instead of being silently coerced.
	tags := registry.RootTagSet().With("expected_response", "yes")

	row := sampleToRow(metrics.Sample{
		TimeSeries: metrics.TimeSeries{Metric: m, Tags: tags},
		Time:       time.Now(),
		Value:      1,
	})

	assert.Nil(t, row.ExpectedResponse)
	assert.Equal(t, "yes", row.ExtraTags["expected_response"])
}
