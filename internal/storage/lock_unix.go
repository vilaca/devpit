//go:build unix

package storage

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

// flockFile takes a non-blocking exclusive BSD advisory lock (flock) on the
// file at path, creating it if needed. A lock file separate from the database
// keeps this independent of SQLite's own fcntl byte-range locks. Returns
// errLocked if another process already holds the lock.
func flockFile(path string) (*fileLock, error) {
	//nolint:gosec // path is derived from the configured DB path, not external input.
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open lock file %q: %w", path, err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, errLocked
		}
		return nil, fmt.Errorf("flock %q: %w", path, err)
	}
	return &fileLock{f: f}, nil
}
