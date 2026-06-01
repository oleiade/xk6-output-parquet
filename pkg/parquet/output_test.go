package parquet

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/parquet-go/parquet-go"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.k6.io/k6/metrics"
	"go.k6.io/k6/output"
)

// newTestOutput builds an Output suitable for in-package tests. It bypasses
// the registry (no init() runs in tests) and gives the test full control over
// every config layer.
func newTestOutput(t *testing.T, env map[string]string, configArg string, jsonRaw string) (*Output, error) {
	t.Helper()
	logger := logrus.New()
	logger.SetLevel(logrus.DebugLevel)

	params := output.Params{
		OutputType:     "xk6-parquet",
		Logger:         logger,
		Environment:    env,
		ConfigArgument: configArg,
	}
	if jsonRaw != "" {
		params.JSONConfig = json.RawMessage(jsonRaw)
	}

	o, err := New(params)
	if err != nil {
		return nil, err
	}
	return o.(*Output), nil
}

func TestNew_OK(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "run.parquet")
	o, err := newTestOutput(t, nil, path, "")
	require.NoError(t, err)
	require.NotNil(t, o)
	assert.Equal(t, path, o.config.FilePath.String)
	assert.Equal(t, defaultPushInterval, o.config.PushInterval.TimeDuration())
	assert.Equal(t, defaultCompression, o.config.Compression.String)
}

func TestNew_RejectsMissingFilePath(t *testing.T) {
	t.Parallel()

	_, err := newTestOutput(t, nil, "", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "file path is required")
}

func TestDescription(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "run.parquet")
	o, err := newTestOutput(t, nil, path, "")
	require.NoError(t, err)
	assert.Contains(t, o.Description(), "parquet")
	assert.Contains(t, o.Description(), path)
}

// TestLifecycle_RoundTrip exercises the full Start → AddMetricSamples → Stop
// flow and then reads the resulting Parquet file back with parquet-go to
// verify the rows landed on disk with the expected schema.
func TestLifecycle_RoundTrip(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "run.parquet")
	o, err := newTestOutput(t,
		map[string]string{"K6_PARQUET_PUSH_INTERVAL": "10ms"},
		path,
		"",
	)
	require.NoError(t, err)

	require.NoError(t, o.Start())

	registry := metrics.NewRegistry()
	durationMetric, err := registry.NewMetric("http_req_duration", metrics.Trend, metrics.Time)
	require.NoError(t, err)
	counterMetric, err := registry.NewMetric("iterations", metrics.Counter)
	require.NoError(t, err)

	now := time.Now()
	rootTags := registry.RootTagSet()
	httpTags := rootTags.
		With("scenario", "default").
		With("group", "").
		With("method", "GET").
		With("status", "200").
		With("url", "https://example.com/api").
		With("name", "https://example.com/api").
		With("proto", "HTTP/1.1").
		With("expected_response", "true").
		With("custom_tag", "user-supplied")

	o.AddMetricSamples([]metrics.SampleContainer{
		metrics.Samples([]metrics.Sample{
			{
				TimeSeries: metrics.TimeSeries{Metric: durationMetric, Tags: httpTags},
				Time:       now,
				Value:      42.5,
				Metadata:   map[string]string{"vu": "3", "iter": "17"},
			},
			{
				TimeSeries: metrics.TimeSeries{Metric: counterMetric, Tags: rootTags},
				Time:       now.Add(time.Millisecond),
				Value:      1,
			},
		}),
	})

	require.NoError(t, o.Stop())

	// File must exist and be non-empty.
	st, err := os.Stat(path)
	require.NoError(t, err)
	assert.Greater(t, st.Size(), int64(0))

	// Read back with parquet-go and verify rows + KV metadata.
	rows, kv, err := readBack(t, path)
	require.NoError(t, err)
	require.Len(t, rows, 2)

	var trendRow, counterRow *sampleRow
	for i := range rows {
		switch rows[i].MetricName {
		case "http_req_duration":
			trendRow = &rows[i]
		case "iterations":
			counterRow = &rows[i]
		}
	}
	require.NotNil(t, trendRow, "trend row missing")
	require.NotNil(t, counterRow, "counter row missing")

	assert.Equal(t, "trend", trendRow.MetricType)
	assert.Equal(t, "time", trendRow.ValueType)
	assert.Equal(t, 42.5, trendRow.Value)
	require.NotNil(t, trendRow.Scenario)
	assert.Equal(t, "default", *trendRow.Scenario)
	require.NotNil(t, trendRow.Method)
	assert.Equal(t, "GET", *trendRow.Method)
	require.NotNil(t, trendRow.Status)
	assert.EqualValues(t, 200, *trendRow.Status)
	require.NotNil(t, trendRow.ExpectedResponse)
	assert.True(t, *trendRow.ExpectedResponse)
	require.NotNil(t, trendRow.VU)
	assert.EqualValues(t, 3, *trendRow.VU)
	require.NotNil(t, trendRow.Iter)
	assert.EqualValues(t, 17, *trendRow.Iter)
	assert.Equal(t, map[string]string{"custom_tag": "user-supplied"}, trendRow.ExtraTags)

	assert.Equal(t, "counter", counterRow.MetricType)
	assert.Nil(t, counterRow.Scenario, "counter row had no tags")

	// Footer KV metadata: schema_version, test_run_id, sample_count,
	// compression, etc.
	assert.Equal(t, SchemaVersion, kv[metaPrefix+"schema_version"])
	assert.Equal(t, "zstd", kv[metaPrefix+"compression"])
	assert.Equal(t, "2", kv[metaPrefix+"sample_count"])
	assert.NotEmpty(t, kv[metaPrefix+"test_run_id"])
	assert.NotEmpty(t, kv[metaPrefix+"start_time"])
	assert.NotEmpty(t, kv[metaPrefix+"end_time"])
}

// readBack opens the Parquet file at path and returns its rows + KV metadata.
func readBack(t *testing.T, path string) ([]sampleRow, map[string]string, error) {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()

	st, err := f.Stat()
	if err != nil {
		return nil, nil, err
	}

	pf, err := parquet.OpenFile(f, st.Size())
	if err != nil {
		return nil, nil, err
	}

	kv := map[string]string{}
	for _, m := range pf.Metadata().KeyValueMetadata {
		kv[m.Key] = m.Value
	}

	reader := parquet.NewGenericReader[sampleRow](pf)
	defer reader.Close()

	rows := make([]sampleRow, reader.NumRows())
	n, err := reader.Read(rows)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, nil, err
	}
	rows = rows[:n]
	return rows, kv, nil
}

// TestStop_BeforeStart_NoOps pins down that the lifecycle guard lives on the
// Output side: Stop must be safe to call on a freshly-constructed Output that
// never reached Start (e.g. when New succeeded but the engine bailed before
// starting the output). The Client's internal nil-checks were removed in
// favour of this single boundary.
func TestStop_BeforeStart_NoOps(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "run.parquet")
	o, err := newTestOutput(t, nil, path, "")
	require.NoError(t, err)

	require.NoError(t, o.Stop())

	// File should NOT have been created — Start never ran.
	_, statErr := os.Stat(path)
	assert.True(t, os.IsNotExist(statErr), "Stop before Start must not create the output file")
}

func TestCountSamples(t *testing.T) {
	t.Parallel()

	registry := metrics.NewRegistry()
	m, err := registry.NewMetric("test_counter", metrics.Counter)
	require.NoError(t, err)

	containers := []metrics.SampleContainer{
		metrics.Samples([]metrics.Sample{
			{TimeSeries: metrics.TimeSeries{Metric: m}, Value: 1},
			{TimeSeries: metrics.TimeSeries{Metric: m}, Value: 2},
		}),
		metrics.Samples([]metrics.Sample{
			{TimeSeries: metrics.TimeSeries{Metric: m}, Value: 3},
		}),
	}
	assert.Equal(t, 3, countSamples(containers))
	assert.Equal(t, 0, countSamples(nil))
}
