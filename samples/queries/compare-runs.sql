-- A/B regression detection between two runs.
--
-- Reads two parquet files in one go and tags each row with its source file
-- (filename=true), then pivots p95_ms by run for side-by-side comparison.
-- This is the single most valuable query the parquet format unlocks vs.
-- streaming JSON output — historical runs sit on disk, queryable forever.
--
-- Set RUN_A and RUN_B environment variables to the two parquet files:
--   RUN_A=baseline.parquet RUN_B=candidate.parquet \
--     duckdb -c "$(cat samples/queries/compare-runs.sql)"
WITH labelled AS (
    SELECT
        CASE
            WHEN filename = getenv('RUN_A') THEN 'A'
            WHEN filename = getenv('RUN_B') THEN 'B'
        END     AS run,
        name    AS section,
        value
    FROM read_parquet([getenv('RUN_A'), getenv('RUN_B')], filename=true)
    WHERE metric_name = 'http_req_duration'
)
SELECT
    section,
    round(quantile_cont(value, 0.95) FILTER (WHERE run = 'A'), 1) AS a_p95_ms,
    round(quantile_cont(value, 0.95) FILTER (WHERE run = 'B'), 1) AS b_p95_ms,
    round(quantile_cont(value, 0.95) FILTER (WHERE run = 'B')
        - quantile_cont(value, 0.95) FILTER (WHERE run = 'A'), 1) AS delta_ms
FROM labelled
GROUP BY 1
ORDER BY delta_ms DESC NULLS LAST;
