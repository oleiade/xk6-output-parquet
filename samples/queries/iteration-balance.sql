-- Iterations per section — sanity-check the round-robin distribution.
--
-- script.js round-robins SECTIONS by iterationInTest, so with N sections and
-- M total iterations every section should show ~M/N samples (±1 due to
-- integer division and early termination).
--
-- We count http_reqs rather than the iterations metric because iterations
-- fires at iteration end and doesn't carry per-request tags. Since each
-- iteration in script.js makes exactly one http.get, http_reqs is the
-- correct per-section iteration count.
SELECT
    name AS section,
    count(*) AS iterations
FROM read_parquet(getenv('PARQUET_FILE'))
WHERE metric_name = 'http_reqs'
GROUP BY 1
ORDER BY iterations DESC, section;
