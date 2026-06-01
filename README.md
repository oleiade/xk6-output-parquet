# xk6-output-parquet

A [k6](https://k6.io) output extension that writes test-run metrics to a
**Parquet** file, designed for downstream analytics with
[DuckDB](https://duckdb.org), Polars, pandas, or any tool that speaks Parquet.

The file is self-describing: run identity, k6 version, script options,
thresholds, start/end time and sample count are embedded in the Parquet
KV footer, so no sidecar is needed.

## Quick start

```bash
# 1. Build a k6 binary with this extension linked in.
go install go.k6.io/xk6/cmd/xk6@latest
xk6 build --with $(go list -m)=.

# 2. Run a test, writing samples to a Parquet file.
./k6 run samples/script.js -o xk6-parquet=./run.parquet

# 3. Query the file with DuckDB.
duckdb -c "SELECT metric_name, count(*), avg(value)
           FROM read_parquet('run.parquet')
           GROUP BY 1 ORDER BY 2 DESC LIMIT 10;"
```

## Configuration

Configuration is layered (later layers override earlier):

1. Defaults
2. The `parquet` block in a k6 JSON config file passed to `--config`
3. Environment variables (`K6_PARQUET_*`)
4. The argument to `-o xk6-parquet=<path>` on the k6 CLI

### Options

| Env / JSON | Default | Purpose |
|---|---|---|
| `K6_PARQUET_FILE_PATH` / `"filePath"` | _(required)_ | Path to the output `.parquet` file. Also accepted as `-o xk6-parquet=<path>`. `file://` URIs are accepted. |
| `K6_PARQUET_PUSH_INTERVAL` / `"pushInterval"` | `1s` | How often buffered samples are drained into the writer. |
| `K6_PARQUET_COMPRESSION` / `"compression"` | `zstd` | One of `zstd`, `snappy`, `gzip`, `uncompressed`. |
| `K6_PARQUET_ROW_GROUP_SIZE` / `"rowGroupSize"` | `100000` | Max rows per Parquet row group. Bigger groups compress better; smaller groups use less memory at write time. |
| `K6_PARQUET_PAGE_BUFFER_SIZE` / `"pageBufferSize"` | `1048576` (1 MiB) | Size of each Parquet data page, in bytes. |
| `K6_PARQUET_TEST_RUN_ID` / `"testRunId"` | _(random UUID)_ | Run identifier embedded in the file's KV footer. Set this to join the file with external systems (CI run IDs, Grafana annotations, etc.). |
| `K6_PARQUET_TEST_RUN_NAME` / `"testRunName"` | _(empty)_ | Human label for the run (e.g. `"baseline-2026-05"`). |

## File schema

One row per `metrics.Sample`. The schema is wide: the most-queried k6 system
tags are first-class columns, and any tag or sample-metadata not in that set
spills into MAP columns so nothing is lost.

| Column | Type | Notes |
|---|---|---|
| `ts_unix_nano` | TIMESTAMP(ns, UTC) | Sample wall-clock time. |
| `metric_name` | STRING | e.g. `http_req_duration`, `vus`, `iterations`. |
| `metric_type` | STRING | `counter`, `gauge`, `trend`, `rate`. |
| `value_type` | STRING | `default`, `time` (ms), `data` (bytes). |
| `value` | DOUBLE | Raw observation. Trends keep the full distribution — no pre-aggregation. |
| `submetric_parent` | STRING? | Set when sample belongs to a submetric like `http_req_duration{status:200}`. |
| `submetric_suffix` | STRING? | The `{tag:val,…}` clause that defined the submetric. |
| `scenario`, `group`, `service`, `name`, `method`, `url`, `proto`, `subproto`, `tls_version`, `check`, `error`, `ip` | STRING? | Promoted system tags. |
| `status`, `error_code` | INT32? | Parsed from the corresponding tags. |
| `expected_response` | BOOL? | From the `expected_response` tag. |
| `vu`, `iter` | INT64? | From per-sample metadata (k6's non-indexed system tags). |
| `extra_tags` | MAP<STRING,STRING>? | Catch-all for tags not in the promoted set (custom user tags, future system tags). |
| `extra_metadata` | MAP<STRING,STRING>? | Catch-all for metadata other than `vu`/`iter` (e.g. `trace_id`). |

Schema version is recorded in the file's KV footer as
`xk6_output_parquet.schema_version`.

### Footer (key-value) metadata

The Parquet footer carries run-level metadata under the
`xk6_output_parquet.*` prefix:

| Key | Notes |
|---|---|
| `schema_version` | Bumped when the column layout changes. |
| `test_run_id`, `test_run_name` | Run identity. |
| `k6_version`, `go_version`, `os`, `arch`, `hostname` | Build/host fingerprint. |
| `script_path` | Path or URL of the test script. |
| `script_options` | Full `lib.Options` as JSON (thresholds, scenarios, tags…). |
| `start_time`, `end_time`, `duration` | Bookend timestamps. |
| `sample_count` | Total rows written. |
| `compression`, `row_group_size` | Writer settings actually used. |

Inspect from the shell:

```bash
duckdb -c "SELECT * FROM parquet_kv_metadata('run.parquet');"
```

## DuckDB recipes

These run against the file produced above. Adjust the path as needed.

**Top metrics by sample volume:**
```sql
SELECT metric_name, count(*) AS n
FROM 'run.parquet'
GROUP BY 1 ORDER BY 2 DESC;
```

**Per-endpoint latency percentiles** (computed at query time — the raw trend
distribution is in the file):
```sql
SELECT name,
       count(*) AS samples,
       quantile_cont(value, 0.50) AS p50,
       quantile_cont(value, 0.95) AS p95,
       quantile_cont(value, 0.99) AS p99,
       max(value) AS p100
FROM 'run.parquet'
WHERE metric_name = 'http_req_duration'
GROUP BY name
ORDER BY p95 DESC;
```

**Error rate per scenario / status:**
```sql
SELECT scenario,
       status,
       count(*) FILTER (WHERE metric_name = 'http_req_failed' AND value = 1) AS failed,
       count(*) FILTER (WHERE metric_name = 'http_req_failed')                AS total,
       100.0 * failed / NULLIF(total, 0)                                       AS error_pct
FROM 'run.parquet'
GROUP BY scenario, status
ORDER BY error_pct DESC NULLS LAST;
```

**Throughput over time (1-second buckets):**
```sql
SELECT time_bucket(INTERVAL 1 SECOND, ts_unix_nano) AS sec,
       count(*) AS reqs
FROM 'run.parquet'
WHERE metric_name = 'http_reqs'
GROUP BY sec ORDER BY sec;
```

**Compare two runs** (concatenate the files; identify each by `test_run_id`
from the KV metadata or by file name):
```sql
WITH runs AS (
  SELECT *, 'baseline' AS run FROM 'baseline.parquet'
  UNION ALL
  SELECT *, 'candidate' AS run FROM 'candidate.parquet'
)
SELECT run, name,
       quantile_cont(value, 0.95) AS p95
FROM runs
WHERE metric_name = 'http_req_duration'
GROUP BY run, name
ORDER BY name, run;
```

**Failed checks (one row per failure):**
```sql
SELECT ts_unix_nano, scenario, name, check
FROM 'run.parquet'
WHERE metric_name = 'checks' AND value = 0
ORDER BY ts_unix_nano;
```

**Drill into custom tags** (the `extra_tags` MAP column):
```sql
SELECT extra_tags['team'] AS team,
       quantile_cont(value, 0.95) AS p95
FROM 'run.parquet'
WHERE metric_name = 'http_req_duration'
  AND extra_tags['team'] IS NOT NULL
GROUP BY team
ORDER BY p95 DESC;
```

## Development

```bash
make test           # go test -race -cover ./...
make lint           # golangci-lint run ./...
make build          # produces ./k6 with this extension linked in
make it             # 1-iteration smoke test (writes /tmp/xk6-parquet-it.parquet)
```

## How it works

Samples are buffered in memory via `output.SampleBuffer`. A
`output.PeriodicFlusher` drains the buffer every `pushInterval` and hands the
batch to a `parquet.GenericWriter` that streams rows into the destination
file. Row groups are flushed automatically when they reach `rowGroupSize`
rows; the footer (with KV metadata) is written on `Stop()`.

See `AGENTS.md` for the architectural contract and the rules for contributors.

## License

TBD — choose one of Apache-2.0, MIT, or AGPL-3.0 (the latter matches the
Grafana org's `xk6-output-*` repos).
