package storage

import (
	"errors"
	"fmt"
	"os"
)

// fileLock is an advisory single-instance lock held for a database's lifetime.
// Two devpit processes on one SQLite file would each write every poll cycle and
// clobber each other's cursors and sync log, so Open takes this lock and Close
// releases it. It wraps the lock file's handle; releasing closes it, which the
// OS treats as dropping the advisory lock.
type fileLock struct {
	f *os.File
}

// errLocked is returned by flockFile when another process holds the lock. It is
// a sentinel so acquireLock can attach a user-facing message with the DB path.
var errLocked = errors.New("locked by another process")

// acquireLock takes an exclusive advisory lock guarding the database at path.
// In-memory databases are per-process and need no guard, so they get a no-op
// lock; the same is true on platforms without advisory locking (see the
// build-tagged flockFile variants).
func acquireLock(path string) (*fileLock, error) {
	if path == "" || path == ":memory:" {
		return &fileLock{}, nil
	}
	l, err := flockFile(path + ".lock")
	if errors.Is(err, errLocked) {
		return nil, fmt.Errorf("database %q is already in use by another devpit instance", path)
	}
	return l, err
}

// release drops the lock. Safe to call on a nil or no-op lock.
func (l *fileLock) release() error {
	if l == nil || l.f == nil {
		return nil
	}
	return l.f.Close()
}
