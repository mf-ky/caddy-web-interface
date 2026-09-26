//go:build unix

package store

import (
	"os"
	"syscall"
)

// matchOwner gives a file the same owner as its directory when running as
// root. This keeps `sudo caddyweb user passwd ...` from leaving files the
// CaddyWeb service (running as its own user) can no longer read.
func matchOwner(file, dir string) {
	if os.Geteuid() != 0 {
		return
	}
	st, err := os.Stat(dir)
	if err != nil {
		return
	}
	if sys, ok := st.Sys().(*syscall.Stat_t); ok {
		_ = os.Chown(file, int(sys.Uid), int(sys.Gid))
	}
}
