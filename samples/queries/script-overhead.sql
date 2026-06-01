-- group_duration vs http_req_duration, measure script overhead
-- 
-- The overhead_ms column = sleep + JS engine work + check evaluation.
-- Should be close to your sleep(0.1) value (~100ms) plus minor noise.
WITH per_section AS (
    SELECT
      coalesce(name, replace("group", '::', '')) AS section,
      metric_name,
      value
    FROM read_parquet(getenv('PARQUET_FILE'))
    WHERE metric_name IN ('http_req_duration', 'group_duration')
)
SELECT
    section,
    round(avg(value) FILTER (WHERE metric_name='http_req_duration'), 1) AS http_avg_ms,
    round(avg(value) FILTER (WHERE metric_name='group_duration'), 1)    AS group_avg_ms,
    round(avg(value) FILTER (WHERE metric_name='group_duration')
        - avg(value) FILTER (WHERE metric_name='http_req_duration'), 1) AS overhead_ms
FROM per_section
GROUP BY 1
ORDER BY overhead_ms DESC;
