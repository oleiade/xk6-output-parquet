-- Grafana k6 documentation's check pass rate per section (use group, not name)
--
-- check() samples don't inherit the parent request's name tag, they only carry the group tag.
-- k6 prefixes groups with ::, hence the replace.
SELECT
    replace("group", '::', '') AS section,
    "check"                    AS check_name,
    count(*)                   AS n,
    round(avg(value) * 100, 2)         AS pass_pct
FROM read_parquet(getenv('PARQUET_FILE'))
WHERE metric_name = 'checks'
GROUP BY 1, 2
ORDER BY pass_pct ASC, section;
