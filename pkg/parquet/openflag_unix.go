//go:build unix

package parquet

import "syscall"

// oNoFollow refuses to open the path if its final component is a symbolic
// link. Combined with O_CREATE|O_TRUNC this prevents an attacker who can
// pre-plant a symlink in the destination directory from redirecting our
// truncating write to a victim file. See client.go for the call site.
const oNoFollow = syscall.O_NOFOLLOW
