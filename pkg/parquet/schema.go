// Schema definition and conversion for xk6-output-parquet.
//
// The on-disk Parquet schema is intentionally wide: the ~15 most-queried k6
// system tags are promoted to first-class columns, while any remaining tags
// (and per-sample metadata) spill into MAP<STRING,STRING> catch-all columns.
//
// The rationale:
//
//   - Promoted columns get dictionary encoding, native column-level statistics,
//     and predicate pushdown in DuckDB — they make the common analytical
//     queries (filter by scenario/status, group by method/check, etc.) cheap.
//   - The spill MAPs preserve every user-defined tag without forcing the
//     schema to grow when scripts add custom tags.
//   - One row per metric Sample. Trend metrics keep their raw distribution, so
//     percentiles are computed at query time by DuckDB.
//
// Schema version is recorded in the file's KV footer metadata (see metadata.go)
// so downstream tooling can adapt if the layout evolves.
package parquet

import (
	"strconv"

	"go.k6.io/k6/v2/metrics"
)

// SchemaVersion is the on-disk schema version for the Parquet file. Bump this
// (and document the diff) any time you add, remove, rename, or change the type
// of a column in sampleRow. Downstream tooling (DuckDB views, dashboards) keys
// off this value via the `xk6_output_parquet.schema_version` KV metadata.
const SchemaVersion = "1"

// sampleRow is one row of the output Parquet file: one k6 metric Sample.
//
// Promoted columns correspond to k6 system tags (see metrics/system_tag.go).
// Anything not promoted lands in ExtraTags/ExtraMetadata so no information is
// lost. Pointer types are written as OPTIONAL columns (NULL when absent).
type sampleRow struct {
	// Identity ----------------------------------------------------------------

	// TsUnixNano is the sample time in nanoseconds since the Unix epoch,
	// written with the Parquet TIMESTAMP(NANOSECOND, UTC) logical type. DuckDB
	// reads it as TIMESTAMP_NS without any cast.
	TsUnixNano int64 `parquet:"ts_unix_nano,timestamp(nanosecond)"`

	// MetricName is the k6 metric name (e.g. "http_req_duration").
	MetricName string `parquet:"metric_name"`

	// MetricType is one of "counter", "gauge", "trend", "rate".
	MetricType string `parquet:"metric_type"`

	// ValueType is one of "default", "time", "data" — declares the unit
	// semantics of Value ("time" is milliseconds, "data" is bytes).
	ValueType string `parquet:"value_type"`

	// Value is the raw observation. For trend metrics this is the individual
	// sample, not a pre-aggregated percentile — let DuckDB do the aggregation.
	Value float64 `parquet:"value"`

	// Sub-metric -------------------------------------------------------------

	// SubmetricParent is the parent metric's name when this sample belongs to
	// a sub-metric (e.g. http_req_duration{status:200}); NULL otherwise.
	SubmetricParent *string `parquet:"submetric_parent"`

	// SubmetricSuffix is the {tag:value,...} expression the sub-metric was
	// defined with. NULL for top-level metrics.
	SubmetricSuffix *string `parquet:"submetric_suffix"`

	// Promoted k6 system tags ------------------------------------------------

	Scenario         *string `parquet:"scenario"`
	Group            *string `parquet:"group"`
	Service          *string `parquet:"service"`
	Name             *string `parquet:"name"`
	Method           *string `parquet:"method"`
	Status           *int32  `parquet:"status"`
	URL              *string `parquet:"url"`
	Proto            *string `parquet:"proto"`
	Subproto         *string `parquet:"subproto"`
	TLSVersion       *string `parquet:"tls_version"`
	Check            *string `parquet:"check"`
	Error            *string `parquet:"error"`
	ErrorCode        *int32  `parquet:"error_code"`
	ExpectedResponse *bool   `parquet:"expected_response"`
	IP               *string `parquet:"ip"`

	// Non-indexed system metadata -------------------------------------------
	// These live in metrics.Sample.Metadata (not Tags) in k6.

	VU   *int64 `parquet:"vu"`
	Iter *int64 `parquet:"iter"`

	// Spill columns ----------------------------------------------------------
	// Anything not promoted above (custom user tags, future system tags) lands
	// here so downstream tools can still see it.

	ExtraTags     map[string]string `parquet:"extra_tags,optional"`
	ExtraMetadata map[string]string `parquet:"extra_metadata,optional"`
}

// sampleToRow flattens a single metrics.Sample into a sampleRow.
//
// Unknown tags spill into ExtraTags; unknown metadata into ExtraMetadata.
// Numeric tags that fail to parse (e.g. a non-integer status) fall through
// to ExtraTags so the raw value is still recoverable.
func sampleToRow(s metrics.Sample) sampleRow {
	row := sampleRow{
		TsUnixNano: s.Time.UnixNano(),
		Value:      s.Value,
	}
	if s.Metric != nil {
		row.MetricName = s.Metric.Name
		row.MetricType = s.Metric.Type.String()
		row.ValueType = s.Metric.Contains.String()
		if s.Metric.Sub != nil && s.Metric.Sub.Parent != nil {
			parent := s.Metric.Sub.Parent.Name
			row.SubmetricParent = &parent
			suffix := s.Metric.Sub.Suffix
			row.SubmetricSuffix = &suffix
		}
	}

	if s.Tags != nil {
		assignTagsToRow(&row, s.Tags.Map())
	}
	assignMetadataToRow(&row, s.Metadata)
	return row
}

func assignTagsToRow(row *sampleRow, tags map[string]string) {
	var extras map[string]string
	for k, v := range tags {
		switch k {
		case "scenario":
			row.Scenario = stringPtr(v)
		case "group":
			row.Group = stringPtr(v)
		case "service":
			row.Service = stringPtr(v)
		case "name":
			row.Name = stringPtr(v)
		case "method":
			row.Method = stringPtr(v)
		case "url":
			row.URL = stringPtr(v)
		case "proto":
			row.Proto = stringPtr(v)
		case "subproto":
			row.Subproto = stringPtr(v)
		case "tls_version":
			row.TLSVersion = stringPtr(v)
		case "check":
			row.Check = stringPtr(v)
		case "error":
			row.Error = stringPtr(v)
		case "ip":
			row.IP = stringPtr(v)
		case "status":
			if n, ok := parseInt32(v); ok {
				row.Status = &n
			} else {
				extras = putExtra(extras, k, v)
			}
		case "error_code":
			if n, ok := parseInt32(v); ok {
				row.ErrorCode = &n
			} else {
				extras = putExtra(extras, k, v)
			}
		case "expected_response":
			if b, err := strconv.ParseBool(v); err == nil {
				row.ExpectedResponse = &b
			} else {
				extras = putExtra(extras, k, v)
			}
		default:
			extras = putExtra(extras, k, v)
		}
	}
	if len(extras) > 0 {
		row.ExtraTags = extras
	}
}

func assignMetadataToRow(row *sampleRow, md map[string]string) {
	if len(md) == 0 {
		return
	}
	var extras map[string]string
	for k, v := range md {
		switch k {
		case "vu":
			if n, err := strconv.ParseInt(v, 10, 64); err == nil {
				row.VU = &n
			} else {
				extras = putExtra(extras, k, v)
			}
		case "iter":
			if n, err := strconv.ParseInt(v, 10, 64); err == nil {
				row.Iter = &n
			} else {
				extras = putExtra(extras, k, v)
			}
		default:
			extras = putExtra(extras, k, v)
		}
	}
	if len(extras) > 0 {
		row.ExtraMetadata = extras
	}
}

func parseInt32(s string) (int32, bool) {
	n, err := strconv.ParseInt(s, 10, 32)
	if err != nil {
		return 0, false
	}
	return int32(n), true
}

func putExtra(m map[string]string, k, v string) map[string]string {
	if m == nil {
		m = make(map[string]string, 4)
	}
	m[k] = v
	return m
}

func stringPtr(s string) *string { return &s }
