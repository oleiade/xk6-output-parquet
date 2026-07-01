// Run-level metadata embedded as Parquet key-value metadata.
//
// This metadata travels with the file in the Parquet footer, so downstream
// tools that read the file (DuckDB via parquet_kv_metadata, the parquet CLI,
// notebooks, etc.) can recover the run identity, k6 configuration, and any
// thresholds without a sidecar file.
//
// All keys are namespaced under `xk6_output_parquet.` to avoid collisions
// with metadata other tools might write.
package parquet

import (
	"encoding/json"
	"runtime"
	"runtime/debug"
	"strconv"
	"time"

	"go.k6.io/k6/v2/lib"
)

// k6Version returns the version of the linked go.k6.io/k6/v2 module, or
// "unknown" if it can't be resolved from the build info (e.g. tests, or a
// binary built without k6 as a dependency).
func k6Version() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "unknown"
	}
	for _, dep := range info.Deps {
		if dep.Path == "go.k6.io/k6/v2" {
			return dep.Version
		}
	}
	return "unknown"
}

// metaPrefix namespaces every KV metadata key this output writes.
const metaPrefix = "xk6_output_parquet."

// RunMetadata is the snapshot of run-level state recorded in the Parquet
// footer. Populated in Output.Start() and finalised in Output.Stop().
//
// Anything that would help a future analyst answer "what was this run?"
// belongs here. Anything that changes per-sample belongs in the schema.
type RunMetadata struct {
	// SchemaVersion is the on-disk schema version (see schema.go).
	SchemaVersion string

	// TestRunID uniquely identifies this run. Auto-generated UUID unless the
	// user sets K6_PARQUET_TEST_RUN_ID. Lets the file be joined with external
	// systems (Grafana annotations, GitHub Actions run IDs, etc.) and lets a
	// DuckDB query distinguish runs after `read_parquet([...])` over many files.
	TestRunID string

	// TestRunName is an optional human label.
	TestRunName string

	// K6Version is the version of k6 that produced this file.
	K6Version string

	// GoVersion is the Go runtime version used to build the k6 binary.
	GoVersion string

	// OS / Arch capture the host the run executed on.
	OS   string
	Arch string

	// Hostname of the machine running the test, if discoverable.
	Hostname string

	// ScriptPath is the test script as k6 saw it. May be a file path or
	// embedded URL (k6 supports both).
	ScriptPath string

	// StartTime / EndTime bookend the run.
	StartTime time.Time
	EndTime   time.Time

	// SampleCount is the total number of rows written. Populated at close.
	SampleCount int64

	// ScriptOptionsJSON is the k6 lib.Options for the run, serialised to JSON.
	// Includes thresholds, scenarios, tags, etc. Critical for reproducibility.
	ScriptOptionsJSON string

	// Compression is the codec actually used to write the file.
	Compression string

	// RowGroupSize is the configured max rows per row group.
	RowGroupSize int64
}

// newRunMetadata builds a RunMetadata from a (possibly partial) k6 output.Params.
// hostname is passed in so the caller controls the source — we don't want to
// reach out to os.Hostname() from deep in this package without context.
func newRunMetadata(runID, hostname, scriptPath string, opts lib.Options, cfg Config) RunMetadata {
	m := RunMetadata{
		SchemaVersion: SchemaVersion,
		TestRunID:     runID,
		TestRunName:   cfg.TestRunName.String,
		K6Version:     k6Version(),
		GoVersion:     runtime.Version(),
		OS:            runtime.GOOS,
		Arch:          runtime.GOARCH,
		Hostname:      hostname,
		ScriptPath:    scriptPath,
		StartTime:     time.Now().UTC(),
		Compression:   cfg.Compression.String,
		RowGroupSize:  cfg.RowGroupSize.Int64,
	}
	if s, err := safeScriptOptions(opts); err == nil {
		m.ScriptOptionsJSON = s
	}
	return m
}

// footerOptionsAllowlist is the set of lib.Options JSON keys we record in the
// Parquet footer. Whitelist by construction: any field upstream k6 adds is
// invisible here until it is explicitly added below. This is the boundary
// that prevents secret-bearing fields (tlsAuth, cloud, ext) from leaking
// into the footer.
//
// Add a key only after confirming it cannot carry credentials, tokens, or
// other sensitive material.
var footerOptionsAllowlist = []string{
	"vus",
	"duration",
	"iterations",
	"stages",
	"scenarios",
	"thresholds",
	"tags",
	"systemTags",
	"summaryTrendStats",
	"summaryTimeUnit",
}

// safeScriptOptions serialises opts to JSON, keeping only the keys in
// footerOptionsAllowlist. Filtering happens at the JSON layer so we don't
// need to mirror lib.Options' field types in our own struct.
func safeScriptOptions(opts lib.Options) (string, error) {
	raw, err := json.Marshal(opts)
	if err != nil {
		return "", err
	}
	var all map[string]json.RawMessage
	if err := json.Unmarshal(raw, &all); err != nil {
		return "", err
	}
	out := make(map[string]json.RawMessage, len(footerOptionsAllowlist))
	for _, k := range footerOptionsAllowlist {
		if v, ok := all[k]; ok {
			out[k] = v
		}
	}
	b, err := json.Marshal(out)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// KeyValueMetadata renders the RunMetadata as the flat string map Parquet
// stores in its footer.
//
// Time values use RFC3339Nano so they survive the parquet → DuckDB round-trip
// as standard timestamps (DuckDB's `from_iso8601_timestamp` parses them
// directly when querying the KV metadata).
func (m RunMetadata) KeyValueMetadata() map[string]string {
	kv := map[string]string{
		metaPrefix + "schema_version": m.SchemaVersion,
		metaPrefix + "test_run_id":    m.TestRunID,
		metaPrefix + "k6_version":     m.K6Version,
		metaPrefix + "go_version":     m.GoVersion,
		metaPrefix + "os":             m.OS,
		metaPrefix + "arch":           m.Arch,
		metaPrefix + "start_time":     m.StartTime.Format(time.RFC3339Nano),
		metaPrefix + "sample_count":   strconv.FormatInt(m.SampleCount, 10),
		metaPrefix + "compression":    m.Compression,
		metaPrefix + "row_group_size": strconv.FormatInt(m.RowGroupSize, 10),
	}
	if m.TestRunName != "" {
		kv[metaPrefix+"test_run_name"] = m.TestRunName
	}
	if m.Hostname != "" {
		kv[metaPrefix+"hostname"] = m.Hostname
	}
	if m.ScriptPath != "" {
		kv[metaPrefix+"script_path"] = m.ScriptPath
	}
	if !m.EndTime.IsZero() {
		kv[metaPrefix+"end_time"] = m.EndTime.Format(time.RFC3339Nano)
		kv[metaPrefix+"duration"] = m.EndTime.Sub(m.StartTime).String()
	}
	if m.ScriptOptionsJSON != "" {
		kv[metaPrefix+"script_options"] = m.ScriptOptionsJSON
	}
	return kv
}
