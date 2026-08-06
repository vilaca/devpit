package storage

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	// Register the modernc pure-Go sqlite driver for database/sql.
	_ "modernc.org/sqlite"
)

// timeFormat is RFC 3339 UTC, matching the storage schema.
const timeFormat = time.RFC3339

// readMaxConns caps concurrent API reads (ADR-0007). A single-user app never
// needs many; the point is only that reads run on their own pool so a long
// reconcile write never stalls GET /attention.
const readMaxConns = 4

// DB owns two connection pools over the same SQLite database, both in WAL
// (ADR-0007): a single-writer pool (MaxOpenConns 1) the engine uses for all
// mutations, and a read-only pool the API uses for GET queries. Splitting them
// means a long reconcile write never blocks a read and vice versa (ADR-0007).
// Write methods route to write; read methods route to read.
type DB struct {
	write *sql.DB
	read  *sql.DB
	lock  *fileLock
}

// memCounter uniquely names each in-memory database so concurrent Open(":memory:")
// calls (e.g. parallel tests) get isolated databases while the write and read
// pools of a single Open still share one shared-cache instance.
var memCounter atomic.Uint64

// Open opens (or creates) the SQLite database at path in WAL mode, runs any
// pending migrations, and returns a handle exposing a single-writer pool and a
// read-only pool (ADR-0007).
func Open(path string) (*DB, error) {
	// Single-instance guard: refuse to open a file another devpit already owns.
	// Two engines writing one database would clobber each other every cycle.
	lock, err := acquireLock(path)
	if err != nil {
		return nil, err
	}

	writeDSN, readDSN := dsns(path)

	write, err := sql.Open("sqlite", writeDSN)
	if err != nil {
		_ = lock.release()
		return nil, fmt.Errorf("open sqlite (write): %w", err)
	}
	// Single writer (ADR-0007): SQLite permits one writer at a time, so serialising
	// through one connection avoids SQLITE_BUSY on the write path entirely.
	write.SetMaxOpenConns(1)

	db := &DB{write: write, lock: lock}
	if err := db.migrate(context.Background()); err != nil {
		_ = write.Close()
		_ = lock.release()
		return nil, err
	}

	read, err := sql.Open("sqlite", readDSN)
	if err != nil {
		_ = write.Close()
		_ = lock.release()
		return nil, fmt.Errorf("open sqlite (read): %w", err)
	}
	read.SetMaxOpenConns(readMaxConns)
	db.read = read
	return db, nil
}

// dsns builds the write and read DSNs for path. Both carry a busy_timeout so a
// momentary lock waits rather than failing; the read DSN adds query_only as a
// guard against accidental writes on the reader pool. WAL is a persisted
// database property, so only the writer sets journal_mode. An in-memory path is
// rewritten to a uniquely-named shared-cache DSN so the two pools observe the
// same data (two plain ":memory:" opens would be distinct databases).
func dsns(path string) (write, read string) {
	if path == ":memory:" || path == "" {
		name := fmt.Sprintf("devpit_mem_%d", memCounter.Add(1))
		base := "file:" + name + "?mode=memory&cache=shared"
		return base + "&_pragma=busy_timeout(5000)",
			base + "&_pragma=busy_timeout(5000)&_pragma=query_only(true)"
	}
	sep := "?"
	if strings.Contains(path, "?") {
		sep = "&"
	}
	return path + sep + "_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)",
		path + sep + "_pragma=busy_timeout(5000)&_pragma=query_only(true)"
}

// Close closes both pools. It returns the first error encountered but always
// attempts to close both.
func (db *DB) Close() error {
	var firstErr error
	if db.read != nil {
		if err := db.read.Close(); err != nil {
			firstErr = err
		}
	}
	if db.write != nil {
		if err := db.write.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if err := db.lock.release(); err != nil && firstErr == nil {
		firstErr = err
	}
	return firstErr
}

func parseTime(s string) (time.Time, error) {
	return time.Parse(timeFormat, s)
}

func nullInt(p *int) any {
	if p == nil {
		return nil
	}
	return *p
}

func nullStr(p *string) any {
	if p == nil {
		return nil
	}
	return *p
}

// inClause returns n comma-separated "?" placeholders for a SQL IN (...) list.
func inClause(n int) string {
	s := strings.Repeat("?,", n)
	return s[:len(s)-1]
}
