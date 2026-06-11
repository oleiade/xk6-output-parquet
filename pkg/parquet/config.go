// Configuration for xk6-output-parquet.
//
// Three configuration layers, applied in order (later layers override earlier):
//
//  1. Defaults (NewConfig)
//  2. JSON from k6's --config file (output-specific block)
//  3. Environment variables prefixed with K6_PARQUET_
//  4. The argument to -o xk6-parquet=<arg> on the k6 CLI (treated as a file path)
//
// Use null.Type fields so "unset" is distinguishable from "explicitly zero".
package parquet

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"go.k6.io/k6/v2/lib/types"
	"gopkg.in/guregu/null.v3"
)

const (
	// envPrefix is prepended to every environment variable this output reads.
	envPrefix = "K6_PARQUET_"

	// defaultPushInterval matches k6's engine cadence. Larger values trade
	// freshness for bigger, better-compressed row groups.
	defaultPushInterval = 1 * time.Second

	// defaultRowGroupSize is the maximum number of rows in a single Parquet row
	// group. Around 100k rows gives a good size for analytical scans (~MB-scale
	// row groups after ZSTD) without buffering too much in memory.
	defaultRowGroupSize int64 = 100_000

	// defaultCompression is the on-disk compression codec. ZSTD wins on
	// compression ratio vs CPU for the k6 sample shape; DuckDB reads it natively.
	defaultCompression = "zstd"

	// defaultPageBufferSize sizes individual data pages. 1 MiB is a sensible
	// default for analytical workloads.
	defaultPageBufferSize = 1 << 20
)

// CompressionCodec is a typed enum of the supported codecs. Validation
// converts user input into one of these.
type CompressionCodec string

const (
	CompressionZSTD         CompressionCodec = "zstd"
	CompressionSnappy       CompressionCodec = "snappy"
	CompressionGzip         CompressionCodec = "gzip"
	CompressionUncompressed CompressionCodec = "uncompressed"
)

// Config is the consolidated configuration for the output. Every field is a
// null.* type so unset is distinguishable from the zero value.
type Config struct {
	// FilePath is the destination Parquet file. Required.
	//
	// Accepted via the -o xk6-parquet=<path> CLI argument, the
	// K6_PARQUET_FILE_PATH env var, or "filePath" in JSON. Plain paths and
	// file:// URIs both work.
	FilePath null.String `json:"filePath"`

	// PushInterval is how often buffered samples are flushed to the row buffer.
	// The writer also flushes a row group when it reaches RowGroupSize, so this
	// only really gates how often the buffer is drained.
	PushInterval types.NullDuration `json:"pushInterval"`

	// Compression is the Parquet compression codec. One of: zstd, snappy,
	// gzip, uncompressed. Defaults to zstd.
	Compression null.String `json:"compression"`

	// RowGroupSize is the maximum number of rows in a single Parquet row group.
	// The writer auto-flushes a row group once it reaches this many rows.
	// Defaults to 100000.
	RowGroupSize null.Int `json:"rowGroupSize"`

	// PageBufferSize sizes individual data pages, in bytes. Defaults to 1 MiB.
	PageBufferSize null.Int `json:"pageBufferSize"`

	// TestRunID identifies this run inside the file's KV metadata. If unset, a
	// random UUID is generated at Start time so every run can be told apart
	// (useful when concatenating files in DuckDB for comparison).
	TestRunID null.String `json:"testRunId"`

	// TestRunName is a human-friendly label embedded in the file footer.
	// Optional but recommended; surfaces in `parquet meta` and DuckDB queries
	// against the file's KV metadata.
	TestRunName null.String `json:"testRunName"`
}

// NewConfig returns a Config populated with defaults.
func NewConfig() Config {
	return Config{
		FilePath:       null.NewString("", false),
		PushInterval:   types.NewNullDuration(defaultPushInterval, false),
		Compression:    null.NewString(defaultCompression, false),
		RowGroupSize:   null.NewInt(defaultRowGroupSize, false),
		PageBufferSize: null.NewInt(defaultPageBufferSize, false),
		TestRunID:      null.NewString("", false),
		TestRunName:    null.NewString("", false),
	}
}

// Validate returns an error if the consolidated configuration is unusable.
func (c Config) Validate() error {
	if !c.FilePath.Valid || c.FilePath.String == "" {
		return fmt.Errorf("file path is required (pass -o xk6-parquet=<path> or set %sFILE_PATH)", envPrefix)
	}
	if c.PushInterval.TimeDuration() <= 0 {
		return fmt.Errorf("pushInterval must be > 0, got %s", c.PushInterval.TimeDuration())
	}
	if c.RowGroupSize.Int64 <= 0 {
		return fmt.Errorf("rowGroupSize must be > 0, got %d", c.RowGroupSize.Int64)
	}
	if c.PageBufferSize.Int64 <= 0 {
		return fmt.Errorf("pageBufferSize must be > 0, got %d", c.PageBufferSize.Int64)
	}
	if _, err := codecFor(CompressionCodec(c.Compression.String)); err != nil {
		return fmt.Errorf("compression must be one of zstd, snappy, gzip, uncompressed; got %q", c.Compression.String)
	}
	return nil
}

// getConsolidatedConfig merges defaults <- JSON <- env <- CLI path. Each
// layer is applied in place on top of the running cfg, so a field set by an
// earlier layer survives unless an explicit value in a later layer overrides
// it. The null.* types make this work for free: json.Unmarshal only sets
// Valid=true for keys present in the input, and the env/CLI helpers only
// touch fields whose variables/arguments are actually provided.
func getConsolidatedConfig(jsonRaw json.RawMessage, env map[string]string, confArg string) (Config, error) {
	cfg := NewConfig()

	if len(jsonRaw) > 0 {
		if err := json.Unmarshal(jsonRaw, &cfg); err != nil {
			return Config{}, fmt.Errorf("parsing JSON config: %w", err)
		}
	}

	if err := applyEnv(env, &cfg); err != nil {
		return Config{}, fmt.Errorf("parsing environment: %w", err)
	}

	if confArg != "" {
		if err := applyConfigArg(confArg, &cfg); err != nil {
			return Config{}, fmt.Errorf("parsing config argument %q: %w", confArg, err)
		}
	}

	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// envField binds an env-var suffix (after envPrefix) to a Config setter.
// Empty values are treated as "unset" for string-typed fields via the
// per-setter guard; numeric/duration fields propagate parse errors so the
// caller can wrap them with the variable name.
type envField struct {
	name string
	set  func(*Config, string) error
}

var envFields = []envField{
	{"FILE_PATH", func(c *Config, v string) error {
		if v == "" {
			return nil
		}
		c.FilePath = null.StringFrom(v)
		return nil
	}},
	{"PUSH_INTERVAL", func(c *Config, v string) error {
		d, err := time.ParseDuration(v)
		if err != nil {
			return err
		}
		c.PushInterval = types.NewNullDuration(d, true)
		return nil
	}},
	{"COMPRESSION", func(c *Config, v string) error {
		c.Compression = null.StringFrom(strings.ToLower(v))
		return nil
	}},
	{"ROW_GROUP_SIZE", func(c *Config, v string) error {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return err
		}
		c.RowGroupSize = null.IntFrom(n)
		return nil
	}},
	{"PAGE_BUFFER_SIZE", func(c *Config, v string) error {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return err
		}
		c.PageBufferSize = null.IntFrom(n)
		return nil
	}},
	{"TEST_RUN_ID", func(c *Config, v string) error {
		if v == "" {
			return nil
		}
		c.TestRunID = null.StringFrom(v)
		return nil
	}},
	{"TEST_RUN_NAME", func(c *Config, v string) error {
		if v == "" {
			return nil
		}
		c.TestRunName = null.StringFrom(v)
		return nil
	}},
}

// applyEnv reads K6_PARQUET_* variables out of `env` and writes them into c
// in place. Only variables present in env touch c; everything else keeps the
// value it already had from the previous layer.
func applyEnv(env map[string]string, c *Config) error {
	for _, f := range envFields {
		v, ok := env[envPrefix+f.name]
		if !ok {
			continue
		}
		if err := f.set(c, v); err != nil {
			return fmt.Errorf("invalid %s%s=%q: %w", envPrefix, f.name, v, err)
		}
	}
	return nil
}

// applyConfigArg parses the argument passed to `-o xk6-parquet=<arg>` as a
// file path and writes it into c. Plain paths and file:// URIs are both
// accepted.
func applyConfigArg(arg string, c *Config) error {
	path := arg
	if strings.HasPrefix(arg, "file://") {
		u, err := url.Parse(arg)
		if err != nil {
			return fmt.Errorf("not a valid file URI: %w", err)
		}
		path = u.Path
	}
	if path == "" {
		return fmt.Errorf("file path is empty")
	}
	c.FilePath = null.StringFrom(path)
	return nil
}
