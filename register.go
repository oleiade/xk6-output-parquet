// Package parquet registers the xk6-output-parquet extension with k6.
//
// This file is intentionally tiny: it must only call output.RegisterExtension
// in init() so that the extension is wired up when k6 starts. All implementation
// lives in pkg/parquet/.
package parquet

import (
	"github.com/grafana/xk6-output-parquet/pkg/parquet"
	"go.k6.io/k6/v2/output"
)

func init() {
	output.RegisterExtension("xk6-parquet", func(p output.Params) (output.Output, error) {
		return parquet.New(p)
	})
}
