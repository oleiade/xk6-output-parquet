# DuckDB query samples

Ready-to-run DuckDB queries against the Parquet file produced by
`xk6-output-parquet`. They double as worked examples of how the schema
(see [`pkg/parquet/schema.go`](../../pkg/parquet/schema.go)) maps to the
analytical questions a k6 user typically asks.

## Running a query

Every query reads the file pointed to by the `PARQUET_FILE` environment
variable. Run from the repo root:

```sh
PARQUET_FILE=run.parquet duckdb -c "$(cat samples/queries/latency-percentiles-per-section.sql)"
```

Point `PARQUET_FILE` at any other run to re-use the same queries:

```sh
PARQUET_FILE=runs/baseline.parquet duckdb -c "$(cat samples/queries/file-metadata.sql)"
```

The `compare-runs.sql` query is the exception — it takes `RUN_A` and `RUN_B`
instead:

```sh
RUN_A=runs/baseline.parquet RUN_B=runs/candidate.parquet \
    duckdb -c "$(cat samples/queries/compare-runs.sql)"
```

## Query index

| File                                  | Insight                                              | Schema columns relied on                            |
| ------------------------------------- | ---------------------------------------------------- | --------------------------------------------------- |
| `latency-percentiles-per-section.sql` | p50/p95/p99/max of `http_req_duration` per section.  | `metric_name`, `name`, `value`                      |
| `request-phase-per-section.sql`       | Where each section spends its time (TTFB vs recv…).  | `metric_name`, `name`, `value`                      |
| `check-per-section.sql`               | `check()` pass rate per section.                     | `metric_name`, `"group"`, `"check"`, `value`        |
| `throughput-p95-concurrency.sql`      | Throughput, p95 latency, VU count over time.         | `ts_unix_nano`, `metric_name`, `value`              |
| `script-overhead.sql`                 | `group_duration` − `http_req_duration` per section.  | `metric_name`, `name`, `"group"`, `value`           |
| `status-distribution.sql`             | HTTP status codes per section (catches redirects).   | `metric_name`, `name`, `status`                     |
| `error-rate-per-section.sql`          | `http_req_failed` fraction per section.              | `metric_name`, `name`, `value`                      |
| `latency-histogram.sql`               | ASCII histogram revealing bimodal distributions.     | `metric_name`, `value`                              |
| `slowest-requests.sql`                | Top-20 individual outliers with timestamp and tags.  | `ts_unix_nano`, `metric_name`, `name`, `value`      |
| `iteration-balance.sql`               | Iterations per section — round-robin sanity check.   | `metric_name`, `name`                               |
| `compare-runs.sql`                    | A/B p95 delta across two runs.                       | `filename`, `metric_name`, `name`, `value`          |
| `file-metadata.sql`                   | Run provenance from the Parquet footer.              | `parquet_kv_metadata` (footer, not row data)        |

## Schema crib sheet

Full reference lives in [`pkg/parquet/schema.go`](../../pkg/parquet/schema.go).
A pragmatic summary for query authors:

- **One row per `metrics.Sample`.** Trend metrics keep raw observations;
  percentiles are computed at query time with `quantile_cont`.
- **Promoted columns** for the common k6 system tags get dictionary encoding
  and column statistics: `scenario`, `group`, `name`, `method`, `status`,
  `url`, `check`, `error`, `expected_response`, `ip`, plus a few more.
- **Spill columns** (`extra_tags`, `extra_metadata`) are `MAP<VARCHAR, VARCHAR>`
  and hold any custom tag the script attached. Query with
  `extra_tags['my_tag']`.
- **Timestamps** (`ts_unix_nano`) are `TIMESTAMP(NANOSECOND, UTC)`; DuckDB
  reads them as `TIMESTAMP_NS` natively — `time_bucket` works directly.
- **Footer metadata** carries run-level provenance under the
  `xk6_output_parquet.` prefix (`test_run_id`, `schema_version`, `k6_version`,
  `start_time`, `end_time`, `sample_count`, …). See
  [`pkg/parquet/metadata.go`](../../pkg/parquet/metadata.go).

### Tag-coverage gotchas

Two surprises in the schema → query mapping that the samples already work
around:

1. **`check()` samples carry `"group"`, not `name`.** Queries grouping checks
   by section must use `"group"` (and strip the leading `::` k6 prefixes).
2. **`data_received` and `data_sent` carry neither `name` nor `"group"`.**
   They're emitted at the transport layer, not per request, so per-section
   bandwidth is not computable from them.

### SQL quoting

`group` and `check` are SQL reserved words. They must be quoted as
`"group"` and `"check"` in DuckDB.

## DuckDB version notes

- `time_bucket()` requires DuckDB ≥ 0.10.
- `parquet_kv_metadata()` returns `key`/`value` as `BLOB`; decode with
  `decode(col)` to render as text (`file-metadata.sql` does this).
- `read_parquet([...], filename=true)` (used by `compare-runs.sql`) is
  supported on all recent DuckDB versions.
