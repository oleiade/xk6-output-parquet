-- HTTP error rate per section.
--
-- http_req_failed is a rate metric: 1 = failed per k6's expectedResponses
-- logic, 0 = ok. avg() yields the failure fraction; * 100 converts to a
-- percentage. Worth running even when it returns 0 — it proves the dataset
-- is clean rather than leaving the assumption implicit.
SELECT
    name AS section,
    count(*) AS requests,
    round(avg(value) * 100, 2) AS error_pct
FROM read_parquet(getenv('PARQUET_FILE'))
WHERE metric_name = 'http_req_failed'
GROUP BY 1
ORDER BY error_pct DESC, section;
