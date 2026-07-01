# xk6-output-parquet

A [k6](https://k6.io) output extension that writes **every raw metric sample**
from a test run to a single, self-describing **Parquet** file, ready to query
with [DuckDB](https://duckdb.org), Polars, pandas, or anything that reads
Parquet.

## Why use it

k6's built-in outputs hand you a *summary*: aggregated thresholds, end-of-test
percentiles, or a stream into a time-series database you have to run and
maintain. That is great for dashboards, but it throws away the individual
observations. Once the run ends you can no longer ask a question nobody
pre-computed.

This extension keeps the full dataset instead. One row per sample, no
pre-aggregation, trends preserved in full. That unlocks workflows the summary
cannot:

- **Ask arbitrary questions after the run.** Tail-latency outliers,
  per-endpoint regressions, correlations between status codes and timing, the
  exact moment p95 broke. Write the SQL when you have the question, not before.
- **Let an AI agent analyze the whole run.** Point an agent at the Parquet file
  and let it run DuckDB SQL over the complete sample set. It reasons over every
  request, check, and data point rather than a lossy text summary, so it can
  answer follow-ups a digest would have discarded.
- **Compare runs offline.** Files are portable and concatenate cleanly. A/B two
  runs, or track a metric across a CI history, with no database.
- **Zero infrastructure.** A file, not a service. Archive it, attach it to a CI
  artifact, copy it to a teammate. DuckDB queries it in place.

Parquet is columnar and compressed, so the full dataset still scans fast and
stays small.

## Quick start

```bash
# 1. Build a k6 binary with this extension linked in.
go install go.k6.io/xk6/cmd/xk6@latest
xk6 build --with $(go list -m)=.        # or: make build

# 2. Run a test, writing samples to a Parquet file.
./k6 run samples/script.js -o xk6-parquet=./run.parquet

# 3. Query the file with DuckDB.
duckdb -c "SELECT name,
                  count(*)                     AS samples,
                  quantile_cont(value, 0.95)   AS p95
           FROM 'run.parquet'
           WHERE metric_name = 'http_req_duration'
           GROUP BY name ORDER BY p95 DESC;"
```

The percentiles above are computed *at query time* from the raw trend
distribution stored in the file. Nothing was aggregated on write.

More ready-to-run queries live in
[`samples/queries/`](samples/queries/README.md) (latency histograms, error
rates, throughput over time, run comparison, and more).

## Configuration

Layered, later layers override earlier:

1. Defaults
2. The `parquet` block in a k6 JSON config passed to `--config`
3. Environment variables (`K6_PARQUET_*`)
4. The argument to `-o xk6-parquet=<path>` on the k6 CLI

| Env / JSON | Default | Purpose |
|---|---|---|
| `K6_PARQUET_FILE_PATH` / `"filePath"` | _(required)_ | Output `.parquet` path. Also `-o xk6-parquet=<path>`. `file://` URIs accepted. |
| `K6_PARQUET_PUSH_INTERVAL` / `"pushInterval"` | `1s` | How often buffered samples drain into the writer. |
| `K6_PARQUET_COMPRESSION` / `"compression"` | `zstd` | One of `zstd`, `snappy`, `gzip`, `uncompressed`. |
| `K6_PARQUET_ROW_GROUP_SIZE` / `"rowGroupSize"` | `100000` | Max rows per row group. Bigger compresses better; smaller uses less write-time memory. |
| `K6_PARQUET_PAGE_BUFFER_SIZE` / `"pageBufferSize"` | `1048576` | Size of each data page, in bytes. |
| `K6_PARQUET_TEST_RUN_ID` / `"testRunId"` | _(random UUID)_ | Run identifier in the footer. Set it to join with CI run IDs, Grafana annotations, etc. |
| `K6_PARQUET_TEST_RUN_NAME` / `"testRunName"` | _(empty)_ | Human label for the run, e.g. `"baseline-2026-05"`. |

## File schema

One row per `metrics.Sample`. The most-queried k6 system tags are first-class
columns (dictionary-encoded and predicate-pushable in DuckDB); anything else
spills into `MAP` columns, so nothing is lost.

| Column | Type | Notes |
|---|---|---|
| `ts_unix_nano` | TIMESTAMP(ns, UTC) | Sample wall-clock time. |
| `metric_name` | STRING | e.g. `http_req_duration`, `vus`, `iterations`. |
| `metric_type` | STRING | `counter`, `gauge`, `trend`, `rate`. |
| `value_type` | STRING | `default`, `time` (ms), `data` (bytes). |
| `value` | DOUBLE | Raw observation. Trends keep the full distribution. |
| `submetric_parent` | STRING? | Set for submetric samples like `http_req_duration{status:200}`. |
| `submetric_suffix` | STRING? | The `{tag:val,…}` clause that defined the submetric. |
| `scenario`, `group`, `service`, `name`, `method`, `url`, `proto`, `subproto`, `tls_version`, `check`, `error`, `ip` | STRING? | Promoted system tags. |
| `status`, `error_code` | INT32? | Parsed from the matching tags. |
| `expected_response` | BOOL? | From the `expected_response` tag. |
| `vu`, `iter` | INT64? | From per-sample metadata. |
| `extra_tags` | MAP<STRING,STRING>? | Custom user tags and any unpromoted tag. Query with `extra_tags['team']`. |
| `extra_metadata` | MAP<STRING,STRING>? | Metadata other than `vu`/`iter` (e.g. `trace_id`). |

### Footer metadata

The Parquet footer carries run-level provenance under `xk6_output_parquet.*`:
`schema_version`, `test_run_id`, `test_run_name`, `k6_version`, `go_version`,
`os`, `arch`, `hostname`, `script_path`, `script_options` (full `lib.Options`
as JSON), `start_time`, `end_time`, `duration`, `sample_count`, and the
`compression` / `row_group_size` actually used. The file is self-describing, so
analytics need no sidecar.

```bash
duckdb -c "SELECT * FROM parquet_kv_metadata('run.parquet');"
```

## How it works

Samples buffer in memory via `output.SampleBuffer`. An
`output.PeriodicFlusher` drains the buffer every `pushInterval` and streams the
batch through a `parquet.GenericWriter`. Row groups flush automatically at
`rowGroupSize`; the footer (with KV metadata) is written on `Stop()`.

See [`AGENTS.md`](AGENTS.md) for the architectural contract and contributor
rules.

## Development

```bash
make test    # go test -race -cover ./...
make lint    # golangci-lint run ./...
make build   # produces ./k6 with this extension linked in
make it      # 1-iteration smoke test
```

## License

TBD. Choose one of Apache-2.0, MIT, or AGPL-3.0 (the latter matches the
Grafana org's `xk6-output-*` repos).
