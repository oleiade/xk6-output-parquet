package parquet

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetConsolidatedConfig_Defaults(t *testing.T) {
	t.Parallel()

	cfg, err := getConsolidatedConfig(nil, nil, "/tmp/run.parquet")
	require.NoError(t, err)

	assert.Equal(t, "/tmp/run.parquet", cfg.FilePath.String)
	assert.Equal(t, defaultPushInterval, cfg.PushInterval.TimeDuration())
	assert.Equal(t, defaultCompression, cfg.Compression.String)
	assert.Equal(t, defaultRowGroupSize, cfg.RowGroupSize.Int64)
	assert.EqualValues(t, defaultPageBufferSize, cfg.PageBufferSize.Int64)
}

func TestGetConsolidatedConfig_LayerPrecedence(t *testing.T) {
	t.Parallel()

	// JSON layer.
	jsonRaw := []byte(`{"filePath":"/from/json.parquet","pushInterval":"5s","compression":"snappy"}`)
	cfg, err := getConsolidatedConfig(json.RawMessage(jsonRaw), nil, "")
	require.NoError(t, err)
	assert.Equal(t, "/from/json.parquet", cfg.FilePath.String)
	assert.Equal(t, 5*time.Second, cfg.PushInterval.TimeDuration())
	assert.Equal(t, "snappy", cfg.Compression.String)

	// Env overrides JSON.
	cfg, err = getConsolidatedConfig(json.RawMessage(jsonRaw), map[string]string{
		"K6_PARQUET_FILE_PATH":     "/from/env.parquet",
		"K6_PARQUET_PUSH_INTERVAL": "2s",
		"K6_PARQUET_COMPRESSION":   "gzip",
	}, "")
	require.NoError(t, err)
	assert.Equal(t, "/from/env.parquet", cfg.FilePath.String)
	assert.Equal(t, 2*time.Second, cfg.PushInterval.TimeDuration())
	assert.Equal(t, "gzip", cfg.Compression.String)

	// CLI arg overrides env (path only).
	cfg, err = getConsolidatedConfig(json.RawMessage(jsonRaw), map[string]string{
		"K6_PARQUET_FILE_PATH": "/from/env.parquet",
	}, "/from/cli.parquet")
	require.NoError(t, err)
	assert.Equal(t, "/from/cli.parquet", cfg.FilePath.String)
}

func TestGetConsolidatedConfig_FileURI(t *testing.T) {
	t.Parallel()

	cfg, err := getConsolidatedConfig(nil, nil, "file:///tmp/run.parquet")
	require.NoError(t, err)
	assert.Equal(t, "/tmp/run.parquet", cfg.FilePath.String)
}

func TestGetConsolidatedConfig_RejectsBadPushInterval(t *testing.T) {
	t.Parallel()

	_, err := getConsolidatedConfig(nil, map[string]string{
		"K6_PARQUET_PUSH_INTERVAL": "not-a-duration",
	}, "/tmp/run.parquet")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "PUSH_INTERVAL")
}

func TestGetConsolidatedConfig_RejectsBadRowGroupSize(t *testing.T) {
	t.Parallel()

	_, err := getConsolidatedConfig(nil, map[string]string{
		"K6_PARQUET_ROW_GROUP_SIZE": "not-an-int",
	}, "/tmp/run.parquet")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ROW_GROUP_SIZE")
}

func TestGetConsolidatedConfig_RejectsUnknownCompression(t *testing.T) {
	t.Parallel()

	_, err := getConsolidatedConfig(nil, map[string]string{
		"K6_PARQUET_COMPRESSION": "lzma",
	}, "/tmp/run.parquet")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "compression")
}

func TestGetConsolidatedConfig_RejectsMissingFilePath(t *testing.T) {
	t.Parallel()

	_, err := getConsolidatedConfig(nil, nil, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "file path is required")
}

func TestGetConsolidatedConfig_RejectsBadJSON(t *testing.T) {
	t.Parallel()

	_, err := getConsolidatedConfig(json.RawMessage(`not json`), nil, "/tmp/run.parquet")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "JSON config")
}
