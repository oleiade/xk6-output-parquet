-- Throughput, p95, and concurrency as a time series
--
-- time_bucket is DuckDB's native window function, no need to convert to epoch and FLOOR/5*5.
-- Spotting where p95 spikes vs. where VUs ramp tells you whether the system is saturating or coasting.
SELECT
    time_bucket(INTERVAL 5 SECOND, ts_unix_nano)                                                AS bucket,
    count(*) FILTER (WHERE metric_name='http_reqs')                                             AS reqs,
    round(quantile_cont(value, 0.95) FILTER (WHERE metric_name='http_req_duration'), 1) AS p95_ms,
    (max(value) FILTER (WHERE metric_name='vus'))::INT                                          AS vus
FROM read_parquet(getenv('PARQUET_FILE'))
GROUP BY 1
ORDER BY 1;
