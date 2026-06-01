# AGENTS.md

> Notes for AI agents (and humans) working in this repository. This file
> follows the convention used by `xk6-output-influxdb` and similar Grafana
> extensions: a concise architectural spec optimised for an agent loading
> it once at the start of a session.

## What this repository is

`xk6-output-parquet` is a k6 output extension. It is a Go module that, when
compiled into a k6 binary via `xk6 build`, writes test-run metric samples to
a single Parquet file:

```
k6 run --out xk6-parquet=./run.parquet script.js
```

The file is self-describing: run identity, k6 version, host info, script
options/thresholds, start/end time, and sample count are embedded in the
Parquet KV footer. Downstream analytics (DuckDB, Polars, parquet-tools) need
no sidecar.

It is **not** a standalone binary. It only runs inside k6.

## Architecture

```
register.go                      // init() shim: output.RegisterExtension
└── pkg/parquet/
    ├── output.go                // Output struct, lifecycle, flusher callback
    ├── config.go                // Config + getConsolidatedConfig
    ├── schema.go                // sampleRow type + k6 Sample → row conversion
    ├── metadata.go              // RunMetadata + KV footer rendering
    ├── client.go                // Parquet writer lifecycle (open/write/close)
    ├── output_test.go           // Lifecycle + parquet round-trip
    ├── schema_test.go           // Tag promotion / spill conversion
    └── config_test.go           // Config layering + validation
```

## The schema is a contract

`pkg/parquet/schema.go` defines `sampleRow`, the on-disk Parquet schema. It
is what downstream DuckDB queries and dashboards key off of. Treat additive
changes as cheap (new optional column) and destructive changes as
breaking — bump `SchemaVersion` in the same change and document the diff in
the README.

The "promoted columns + spill MAP" pattern is deliberate: ~15 well-known k6
system tags are columns (dictionary-encoded, predicate-pushable in DuckDB),
and anything else lands in `extra_tags` / `extra_metadata`. Don't move a tag
out of the spill columns without considering the cost — every promotion is
forever in the schema.

### Lifecycle (called by the k6 engine)

1. `New(params)` — parse config, return `*Output`. **No I/O here.**
2. `Description() string` — banner text for `k6 run`.
3. `Start() error` — open the Parquet file, build run metadata, start the
   `PeriodicFlusher`. Errors abort the run.
4. `AddMetricSamples(samples)` — inherited from embedded `SampleBuffer`.
   **Never block.**
5. `Stop() error` / `StopWithTestError(err)` — flush remaining samples, write
   the closing KV metadata (end time, sample count), close the writer and the
   file. Blocks until drained.

### Buffering and flushing

We embed `output.SampleBuffer`. Samples accumulate in memory between flushes.
`output.PeriodicFlusher` calls `flushMetrics` every `PushInterval` and exactly
once more on `flusher.Stop()`. Inside `flushMetrics`:

- Read all buffered samples with `o.GetBufferedSamples()`.
- Convert them to `sampleRow` via `sampleToRow` and write through `Client.Send`.
  The parquet writer auto-flushes a row group when it reaches `RowGroupSize`.
- Log per-flush (not per-sample) at Debug level.
- Log Send errors at Error level. Do not retry without a bound.

### Run-level metadata

`RunMetadata` (`metadata.go`) is built in `Output.Start` and embedded in the
Parquet footer as key-value metadata under the `xk6_output_parquet.*` prefix.
It captures the run identity, k6 version, host info, script path and
options/thresholds, start/end time, sample count, and writer settings.

When you add a meaningful run-level field, prefer extending `RunMetadata`
over adding a per-sample column — it's cheaper and more honest semantically.

### Configuration

`getConsolidatedConfig` merges, in order:

1. Defaults from `NewConfig()`.
2. JSON config (`params.JSONConfig`).
3. Environment variables (`K6_PARQUET_*`).
4. The `-o xk6-parquet=<arg>` CLI value (parsed as a file path).

Each layer is applied in place on top of the running `Config`. JSON unmarshals
straight into the running value; env and CLI helpers mutate it via small
setters. New fields require updates in three places: `Config`, `NewConfig`,
and the `envFields` table (plus a test).

## Rules for contributors (and AI agents)

1. **Never block in `AddMetricSamples`.** It is on k6's hot path.
2. **Never panic** in any code path. Wrap risky work; log and return.
3. **Never log per sample.** Per flush is the right granularity.
4. **Never roll your own goroutine + ticker.** Use `PeriodicFlusher`.
5. **Never silently drop samples.** Flush errors must be loud (Error level).
6. **Always implement both `Stop()` and `StopWithTestError`.** The former delegates to the latter.
7. **Always bound retries** — by attempts and by time. `Stop` must return.
8. **Always wrap errors with `%w`** so callers can use `errors.Is`/`errors.As`.
9. **Always add a test** when you add a config field or change a lifecycle method.

## Ideas for future work

- **File rotation** by size or duration, for very long runs where a single
  file becomes unwieldy. Would mean rolling over to `run-0001.parquet`,
  `run-0002.parquet`, … and emitting an index file.
- **Bloom filters** on high-selectivity string columns (`url`, `name`) for
  faster pushdown in DuckDB.
- **`WithThresholds`** support if we want the threshold pass/fail outcome in
  the footer metadata.
- **Streaming dataset layout** (Hive-style partitioning by `scenario` or
  by time bucket) for large fleets of test runs queried together.

Don't pre-build any of these. Only add what a concrete user need requires.

## Useful links

- k6 output package: https://pkg.go.dev/go.k6.io/k6/output
- k6 metrics package: https://pkg.go.dev/go.k6.io/k6/metrics
- xk6 build tool: https://github.com/grafana/xk6
- Reference implementations:
  - https://github.com/grafana/xk6-output-influxdb
  - https://github.com/grafana/xk6-output-prometheus-remote
  - https://github.com/grafana/xk6-output-opentelemetry
  - https://github.com/grafana/xk6-output-example
