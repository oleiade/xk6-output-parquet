-- ASCII histogram of http_req_duration across the whole run.
--
-- Percentiles collapse the distribution to a few points; the shape matters
-- too. A bimodal histogram reveals fast/slow paths (cache hit vs miss,
-- warm vs cold connection, etc.) that p50/p95/p99 alone hide.
--
-- Bucket width is 50ms — adjust to fit your data range. Each '▇' represents
-- 50 samples; scale the divisor if your run is larger or smaller.
SELECT
    (floor(value / 50)::INT * 50)     AS ms_bucket_lower,
    count(*)                          AS n,
    repeat('▇', (count(*) / 50)::INT) AS bar
FROM read_parquet(getenv('PARQUET_FILE'))
WHERE metric_name = 'http_req_duration'
GROUP BY 1
ORDER BY 1;
