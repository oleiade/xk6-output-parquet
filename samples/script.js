// Smoke-test script for xk6-output-parquet.
//
// Round-robins across the top-level sections of the Grafana k6 documentation
// so the resulting Parquet file holds a comparable sample distribution for
// every section. Each request is annotated so DuckDB queries stay cheap:
//
//   - tags.name      -> promoted `name` column (stable label per section, no
//                       URL-cardinality blow-up; uses dictionary encoding +
//                       column statistics for fast filtering / GROUP BY).
//   - group(section) -> promoted `group` column (same slug, redundant on
//                       purpose so either column works in queries).
//   - check()        -> emits the `checks` metric with the promoted `check`
//                       tag, giving per-section pass-rate analytics.
//
// Example queries this layout unlocks:
//
//   -- p95 latency per section
//   SELECT name, quantile_cont(value, 0.95) AS p95_ms
//   FROM read_parquet('run.parquet')
//   WHERE metric_name = 'http_req_duration'
//   GROUP BY 1 ORDER BY p95_ms DESC;
//
//   -- bytes received per section
//   SELECT "group", sum(value) AS bytes
//   FROM read_parquet('run.parquet')
//   WHERE metric_name = 'data_received'
//   GROUP BY 1 ORDER BY bytes DESC;
//
//   -- pass rate per check per section
//   SELECT name, check, avg(value) AS pass_rate
//   FROM read_parquet('run.parquet')
//   WHERE metric_name = 'checks'
//   GROUP BY 1, 2;
//
//   -- request phase breakdown per section
//   SELECT name, metric_name, avg(value)
//   FROM read_parquet('run.parquet')
//   WHERE metric_name LIKE 'http_req_%' AND metric_type = 'trend'
//   GROUP BY 1, 2;

import http from 'k6/http';
import exec from 'k6/execution';
import { check, group, sleep } from 'k6';

const BASE = 'https://grafana.com/docs/k6/latest/';

// Top-level docs sections, as listed on the k6 docs landing page.
const SECTIONS = [
  'get-started',
  'using-k6',
  'using-k6-browser',
  'testing-guides',
  'results-output',
  'javascript-api',
  'reference',
  'examples',
  'extensions',
  'set-up',
  'release-notes',
  'grafana-cloud-k6',
  'k6-studio',
];

export const options = {
  vus: 12,
  iterations: 5000,
};

export default function () {
  // Round-robin across sections so every one receives an equal share of
  // samples (~iterations / SECTIONS.length each). This gives DuckDB a
  // balanced dataset for cross-section comparisons.
  const section = SECTIONS[exec.scenario.iterationInTest % SECTIONS.length];
  const url = BASE + section + '/';

  group(section, () => {
    const res = http.get(url, {
      tags: { name: section },
    });
    check(res, {
      'status is 200': (r) => r.status === 200,
      'body is non-empty': (r) => r.body && r.body.length > 0,
    });
  });
  sleep(0.1);
}
