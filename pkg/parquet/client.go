// Parquet writer client for xk6-output-parquet.
//
// The Client owns the open file handle and the parquet.GenericWriter. It
// receives batches of metric samples from Output.flushMetrics (on the single
// PeriodicFlusher goroutine), flattens them into sampleRow values, and hands
// them to the writer. The writer in turn auto-flushes a row group whenever
// MaxRowsPerRowGroup is reached.
//
// Single-writer invariant: Send is only ever called from the PeriodicFlusher
// goroutine, so this struct is not internally locked. Do not call Send from
// multiple goroutines without adding synchronization.
package parquet

import (
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/parquet-go/parquet-go"
	"github.com/parquet-go/parquet-go/compress"
	"github.com/parquet-go/parquet-go/compress/gzip"
	"github.com/parquet-go/parquet-go/compress/snappy"
	"github.com/parquet-go/parquet-go/compress/uncompressed"
	"github.com/parquet-go/parquet-go/compress/zstd"
	"github.com/sirupsen/logrus"
	"go.k6.io/k6/metrics"
)

// Client wraps the open Parquet file and writer.
type Client struct {
	logger  logrus.FieldLogger
	runMeta RunMetadata
	file    *os.File
	writer  *parquet.GenericWriter[sampleRow]
	rows    int64
}

// NewClient opens the destination file and constructs the Parquet writer.
//
// runMeta is captured by value; its static fields (start time, k6 version,
// script path, etc.) are baked into the initial writer-time KV metadata.
// SampleCount and EndTime are written at Close time via SetKeyValueMetadata.
func NewClient(cfg Config, runMeta RunMetadata, logger logrus.FieldLogger) (*Client, error) {
	codec, err := codecFor(CompressionCodec(cfg.Compression.String))
	if err != nil {
		return nil, err
	}

	f, err := os.OpenFile(cfg.FilePath.String, os.O_CREATE|os.O_WRONLY|os.O_TRUNC|oNoFollow, 0o644)
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w", cfg.FilePath.String, err)
	}

	opts := []parquet.WriterOption{
		parquet.Compression(codec),
		parquet.MaxRowsPerRowGroup(cfg.RowGroupSize.Int64),
		parquet.PageBufferSize(int(cfg.PageBufferSize.Int64)),
		parquet.DataPageStatistics(true),
	}
	for k, v := range runMeta.KeyValueMetadata() {
		opts = append(opts, parquet.KeyValueMetadata(k, v))
	}

	w := parquet.NewGenericWriter[sampleRow](f, opts...)

	return &Client{
		logger:  logger,
		runMeta: runMeta,
		file:    f,
		writer:  w,
	}, nil
}

// Send flattens a batch of k6 sample containers into Parquet rows and writes
// them. The writer buffers rows internally; row groups are flushed when
// MaxRowsPerRowGroup is reached (or on Close).
//
// This is called from a single goroutine (the PeriodicFlusher); concurrent
// callers must serialise externally.
func (c *Client) Send(containers []metrics.SampleContainer) error {
	if len(containers) == 0 {
		return nil
	}

	// Pre-size a single allocation. Most containers carry a handful of samples;
	// guessing 4x containers is a reasonable initial capacity.
	rows := make([]sampleRow, 0, len(containers)*4)
	for _, container := range containers {
		for _, s := range container.GetSamples() {
			rows = append(rows, sampleToRow(s))
		}
	}
	if len(rows) == 0 {
		return nil
	}

	n, err := c.writer.Write(rows)
	if err != nil {
		return fmt.Errorf("writing %d rows to parquet: %w", len(rows), err)
	}
	c.rows += int64(n)
	return nil
}

// RowsWritten returns the running total of rows written through this client.
// Useful for diagnostics and for the closing footer metadata.
func (c *Client) RowsWritten() int64 {
	return c.rows
}

// Close finalises the file: writes the closing run metadata (end time,
// sample count), closes the Parquet writer (which writes the footer), and
// closes the underlying file. k6 calls Stop (and therefore Close) once per
// run; this method is not idempotent.
func (c *Client) Close() (err error) {
	// Stamp the closing metadata. SetKeyValueMetadata replaces the value of an
	// existing key, so this overwrites the start-time placeholders cleanly.
	c.runMeta.EndTime = time.Now().UTC()
	c.runMeta.SampleCount = c.rows
	for k, v := range c.runMeta.KeyValueMetadata() {
		c.writer.SetKeyValueMetadata(k, v)
	}

	defer func() {
		if cerr := c.file.Close(); cerr != nil {
			err = errors.Join(err, fmt.Errorf("closing parquet file: %w", cerr))
		}
	}()

	if werr := c.writer.Close(); werr != nil {
		return fmt.Errorf("closing parquet writer: %w", werr)
	}
	if serr := c.file.Sync(); serr != nil {
		return fmt.Errorf("syncing parquet file: %w", serr)
	}
	return nil
}

// codecFor maps a validated CompressionCodec to a parquet-go compression
// codec. Callers should validate via Config.Validate before reaching this;
// the default branch is kept as defense in depth.
func codecFor(c CompressionCodec) (compress.Codec, error) {
	switch c {
	case CompressionZSTD:
		return &zstd.Codec{}, nil
	case CompressionSnappy:
		return &snappy.Codec{}, nil
	case CompressionGzip:
		return &gzip.Codec{}, nil
	case CompressionUncompressed:
		return &uncompressed.Codec{}, nil
	default:
		return nil, fmt.Errorf("unknown compression codec %q", string(c))
	}
}
