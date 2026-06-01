-- Grafana k6 documentation's request-phase breakdown per section
--
-- The conditional-aggregation pattern (FILTER (WHERE …)) lets you pivot all six phases in a single pass instead of joining six subqueries.
-- The wait_ms column is TTFB, server-think time.
SELECT
    name AS section,
    round(avg(value) FILTER (WHERE metric_name='http_req_blocked'), 1)         AS blocked_ms,
    round(avg(value) FILTER (WHERE metric_name='http_req_connecting'), 1)      AS connect_ms,
    round(avg(value) FILTER (WHERE metric_name='http_req_tls_handshaking'), 1) AS tls_ms,
    round(avg(value) FILTER (WHERE metric_name='http_req_sending'), 2)         AS send_ms,
    round(avg(value) FILTER (WHERE metric_name='http_req_waiting'), 1)         AS wait_ms,
    round(avg(value) FILTER (WHERE metric_name='http_req_receiving'), 1)       AS recv_ms
FROM read_parquet(getenv('PARQUET_FILE'))
WHERE metric_name LIKE 'http_req_%'
AND metric_name NOT IN ('http_req_duration', 'http_req_failed')
AND name IS NOT NULL
GROUP BY 1
ORDER BY wait_ms DESC;
