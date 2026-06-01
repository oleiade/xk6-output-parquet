// Package parquet implements the xk6-output-parquet k6 output extension.
//
// The Output struct satisfies the k6 output.Output interface and writes metric
// samples emitted by k6 into a single Parquet file. Run-level metadata (test
// ID, k6 version, script options, thresholds, start/end time, sample count)
// is embedded in the file's Parquet KV footer, so the file is self-describing
// and analytics on top of it (DuckDB, polars, parquet-tools) need no sidecar.
//
// Lifecycle (called by the k6 engine, in order):
//
//	New(params)          // construct, validate config
//	Description() string // shown in `k6 run` banner
//	Start() error        // open file, build run metadata, start periodic flusher
//	AddMetricSamples(..) // called many times during the run; MUST NOT BLOCK
//	Stop() error         // drain + close (delegates to StopWithTestError)
//
// Buffering and flushing are delegated to output.SampleBuffer +
// output.PeriodicFlusher.
package parquet

import (
	"errors"
	"fmt"
	"os"

	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
	"go.k6.io/k6/lib"
	"go.k6.io/k6/metrics"
	"go.k6.io/k6/output"
)

// Output is the xk6-output-parquet extension.
type Output struct {
	output.SampleBuffer

	config        Config
	scriptPath    string
	scriptOptions lib.Options
	logger        logrus.FieldLogger
	flusher       *output.PeriodicFlusher
	client        *Client
}

// Compile-time assertions that we satisfy the interfaces we claim.
var (
	_ output.Output                = (*Output)(nil)
	_ output.WithStopWithTestError = (*Output)(nil)
)

// New constructs an Output from k6 output.Params. It does NOT open any file
// or spawn goroutines — those belong in Start().
func New(params output.Params) (output.Output, error) {
	cfg, err := getConsolidatedConfig(params.JSONConfig, params.Environment, params.ConfigArgument)
	if err != nil {
		return nil, fmt.Errorf("invalid xk6-output-parquet config: %w", err)
	}
	scriptPath := ""
	if params.ScriptPath != nil {
		scriptPath = params.ScriptPath.String()
	}
	return &Output{
		config:        cfg,
		scriptPath:    scriptPath,
		scriptOptions: params.ScriptOptions,
		logger:        params.Logger,
	}, nil
}

// Description is shown by `k6 run` in its banner. Include the destination
// path so users can verify they're writing to the right file.
func (o *Output) Description() string {
	return fmt.Sprintf("xk6-output-parquet (%s)", o.config.FilePath.String)
}

// Start builds the run metadata, opens the output file, and starts the
// periodic flusher. Any error here aborts the run before VUs are spawned.
func (o *Output) Start() error {
	runID := o.config.TestRunID.String
	if runID == "" {
		runID = uuid.NewString()
	}
	hostname, _ := os.Hostname() // best-effort; empty hostname is fine

	runMeta := newRunMetadata(
		runID,
		o.config.TestRunName.String,
		hostname,
		o.scriptPath,
		o.scriptOptions,
		o.config,
	)

	client, err := NewClient(o.config, runMeta, o.logger)
	if err != nil {
		return fmt.Errorf("xk6-output-parquet: opening writer: %w", err)
	}

	// The flusher's goroutine may fire the callback before NewPeriodicFlusher
	// returns, so the callback closes over the local `client` rather than
	// `o.client` (which isn't set until both resources succeed).
	pf, err := output.NewPeriodicFlusher(o.config.PushInterval.TimeDuration(), func() {
		o.flushMetrics(client)
	})
	if err != nil {
		return errors.Join(
			fmt.Errorf("xk6-output-parquet: starting periodic flusher: %w", err),
			client.Close(),
		)
	}
	o.client = client
	o.flusher = pf

	o.logger.
		WithField("file", o.config.FilePath.String).
		WithField("compression", o.config.Compression.String).
		WithField("row_group_size", o.config.RowGroupSize.Int64).
		WithField("push_interval", o.config.PushInterval.TimeDuration()).
		WithField("test_run_id", runID).
		WithField("schema_version", SchemaVersion).
		Info("Started xk6-output-parquet")
	return nil
}

// AddMetricSamples is inherited from the embedded output.SampleBuffer.
// Do not override it with anything that performs I/O — k6 calls it on a hot
// path and any latency here directly slows down the load test.

// Stop is required by output.Output. We delegate to StopWithTestError so the
// engine sees the same teardown path whether or not it uses the extended
// WithStopWithTestError interface.
func (o *Output) Stop() error {
	return o.StopWithTestError(nil)
}

// StopWithTestError performs the final flush and closes the writer.
func (o *Output) StopWithTestError(testErr error) error {
	o.logger.Debug("Stopping xk6-output-parquet...")
	if testErr != nil {
		o.logger.WithError(testErr).Debug("Test ended with error")
	}

	if o.flusher != nil {
		o.flusher.Stop()
	}

	if o.client != nil {
		if err := o.client.Close(); err != nil {
			return fmt.Errorf("xk6-output-parquet: closing writer: %w", err)
		}
		o.logger.
			WithField("file", o.config.FilePath.String).
			WithField("rows", o.client.RowsWritten()).
			Info("xk6-output-parquet finished writing")
	}
	return nil
}

// flushMetrics is the callback registered with the PeriodicFlusher. It runs
// on every push interval and exactly once more from flusher.Stop(). The
// client is passed explicitly so the callback doesn't depend on Output.client
// being assigned yet (see Start).
func (o *Output) flushMetrics(client *Client) {
	samples := o.GetBufferedSamples()
	if len(samples) == 0 {
		return
	}

	count := countSamples(samples)
	if err := client.Send(samples); err != nil {
		o.logger.
			WithError(err).
			WithField("samples", count).
			WithField("containers", len(samples)).
			Error("Failed to write parquet batch")
		return
	}
	o.logger.
		WithField("samples", count).
		WithField("containers", len(samples)).
		Debug("Wrote parquet batch")
}

// countSamples returns the total number of metric samples across all containers.
func countSamples(containers []metrics.SampleContainer) int {
	var n int
	for _, c := range containers {
		n += len(c.GetSamples())
	}
	return n
}
