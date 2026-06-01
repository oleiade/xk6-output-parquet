--  Grafana k6 documentation's latency percentiles per section
-- 
-- Sorts by p95_ms to find the slowest pages, tail behavior matters more than the mean for user perception.
SELECT
    name AS section,
    count(*)                                      AS samples,
    round(quantile_cont(value, 0.50), 1)          AS p50_ms,
    round(quantile_cont(value, 0.95), 1)          AS p95_ms,
    round(quantile_cont(value, 0.99), 1)          AS p99_ms,
    round(max(value), 1)                          AS max_ms
FROM read_parquet(getenv('PARQUET_FILE'))
WHERE metric_name = 'http_req_duration'
GROUP BY 1
ORDER BY p95_ms DESC;
