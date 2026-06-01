//go:build !unix

package parquet

// oNoFollow is a no-op on platforms without O_NOFOLLOW (notably Windows,
// where the equivalent attack surface differs and the flag is not defined).
const oNoFollow = 0
