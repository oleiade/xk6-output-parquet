-- HTTP status code distribution per section.
--
-- Catches non-200 responses that check() can miss: k6 follows redirects by
-- default, so checks run on the final response. This query reports the actual
-- status code k6 saw on each request.
SELECT
    name AS section,
    status,
    count(*) AS n
FROM read_parquet(getenv('PARQUET_FILE'))
WHERE metric_name = 'http_req_duration'
GROUP BY 1, 2
ORDER BY 1, 2;
