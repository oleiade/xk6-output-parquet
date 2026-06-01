-- Top 20 slowest individual requests with their tags.
--
-- Outlier inspection: tells you which exact request, when, and in which
-- section was hit by a tail-latency spike. Pair with the time-series query
-- (throughput-p95-concurrency.sql) to see whether the spike was isolated
-- or correlated with a bucket-wide degradation.
SELECT
    ts_unix_nano    AS ts,
    name            AS section,
    method,
    status,
    round(value, 1) AS duration_ms
FROM read_parquet(getenv('PARQUET_FILE'))
WHERE metric_name = 'http_req_duration'
ORDER BY value DESC
LIMIT 20;
