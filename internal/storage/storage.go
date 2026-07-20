package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/vilaca/devpit/sdk"

	// Register the modernc pure-Go sqlite driver for database/sql.
	_ "modernc.org/sqlite"
)

// timeFormat is RFC 3339 UTC, matching the storage schema (§4).
const timeFormat = time.RFC3339

// readMaxConns caps concurrent API reads (§14, Q9). A single-user app never
// needs many; the point is only that reads run on their own pool so a long
// reconcile write never stalls GET /attention.
const readMaxConns = 4

// DB owns two connection pools over the same SQLite database, both in WAL
// (§4, §14, Q9): a single-writer pool (MaxOpenConns 1) the engine uses for all
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
// read-only pool (§14, Q9).
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
	// Single writer (§14): SQLite permits one writer at a time, so serialising
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

// WriteEvents inserts events for a connection, deduplicating on
// (connection_id, object_type, native_id, event_type, dedupe_key) via
// INSERT OR IGNORE. Stamps observed_at = now.
// Returns the number of newly inserted rows (for sync_log.items_changed).
func (db *DB) WriteEvents(ctx context.Context, connectionID string, events []sdk.Event) (int, error) {
	if len(events) == 0 {
		return 0, nil
	}

	tx, err := db.write.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("write events begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	stmt, err := tx.PrepareContext(ctx, `
		INSERT OR IGNORE INTO events
			(connection_id, object_type, native_id, event_type,
			 occurred_at, actor, dedupe_key, payload, observed_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return 0, fmt.Errorf("write events prepare: %w", err)
	}
	defer func() { _ = stmt.Close() }()

	observedAt := time.Now().UTC().Format(timeFormat)

	inserted := 0
	for _, e := range events {
		payload, err := marshalPayload(e.Payload)
		if err != nil {
			return 0, fmt.Errorf("marshal payload (native_id %q): %w", e.NativeID, err)
		}

		var occurredAt any
		if e.OccurredAt != nil {
			occurredAt = e.OccurredAt.UTC().Format(timeFormat)
		}

		res, err := stmt.ExecContext(ctx,
			connectionID, e.ObjectType, e.NativeID, e.EventType,
			occurredAt, e.Actor, e.DedupeKey, payload, observedAt)
		if err != nil {
			return 0, fmt.Errorf("insert event (native_id %q): %w", e.NativeID, err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return 0, fmt.Errorf("rows affected: %w", err)
		}
		inserted += int(n)
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("write events commit: %w", err)
	}
	return inserted, nil
}

// marshalPayload serialises the event payload to a JSON string. A nil payload
// (e.g. item.removed) is stored as "{}" to satisfy the NOT NULL column.
func marshalPayload(payload any) (string, error) {
	if payload == nil {
		return "{}", nil
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// LoadCursors loads all cursor key/value pairs for a connection.
// Returns an empty (non-nil) map if none exist.
func (db *DB) LoadCursors(ctx context.Context, connectionID string) (sdk.PollState, error) {
	rows, err := db.read.QueryContext(ctx,
		`SELECT key, value FROM sync_cursors WHERE connection_id = ?`, connectionID)
	if err != nil {
		return nil, fmt.Errorf("load cursors: %w", err)
	}
	defer func() { _ = rows.Close() }()

	state := sdk.PollState{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, fmt.Errorf("scan cursor: %w", err)
		}
		state[k] = v
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("load cursors: %w", err)
	}
	return state, nil
}

// SaveCursors upserts all key/value pairs in state for the connection.
func (db *DB) SaveCursors(ctx context.Context, connectionID string, state sdk.PollState) error {
	if len(state) == 0 {
		return nil
	}

	tx, err := db.write.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("save cursors begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO sync_cursors (connection_id, key, value)
		VALUES (?, ?, ?)
		ON CONFLICT (connection_id, key) DO UPDATE SET value = excluded.value`)
	if err != nil {
		return fmt.Errorf("save cursors prepare: %w", err)
	}
	defer func() { _ = stmt.Close() }()

	for k, v := range state {
		if _, err := stmt.ExecContext(ctx, connectionID, k, v); err != nil {
			return fmt.Errorf("save cursor %q: %w", k, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("save cursors commit: %w", err)
	}
	return nil
}

// SyncLogEntry is one row in the sync_log table.
type SyncLogEntry struct {
	ID            int64
	Ts            time.Time
	ConnectionID  string
	Operation     string
	Outcome       string
	HTTPStatus    *int
	ItemsChanged  int
	RateRemaining *int
	Retries       int
	NextRetry     *time.Time
	Error         *string
}

// WriteSyncLog inserts one sync_log row.
func (db *DB) WriteSyncLog(ctx context.Context, entry SyncLogEntry) error {
	var nextRetry any
	if entry.NextRetry != nil {
		nextRetry = entry.NextRetry.UTC().Format(timeFormat)
	}

	_, err := db.write.ExecContext(ctx, `
		INSERT INTO sync_log
			(ts, connection_id, operation, outcome, http_status,
			 items_changed, rate_remaining, retries, next_retry, error)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		entry.Ts.UTC().Format(timeFormat), entry.ConnectionID, entry.Operation,
		entry.Outcome, nullInt(entry.HTTPStatus), entry.ItemsChanged,
		nullInt(entry.RateRemaining), entry.Retries, nextRetry, nullStr(entry.Error))
	if err != nil {
		return fmt.Errorf("write sync_log: %w", err)
	}
	return nil
}

// ReadSyncLog returns the most recent limit rows for connectionID (or all
// connections if connectionID is ""), newest first.
func (db *DB) ReadSyncLog(ctx context.Context, connectionID string, limit int) ([]SyncLogEntry, error) {
	query := `SELECT id, ts, connection_id, operation, outcome, http_status,
		items_changed, rate_remaining, retries, next_retry, error
		FROM sync_log`
	var args []any
	if connectionID != "" {
		query += ` WHERE connection_id = ?`
		args = append(args, connectionID)
	}
	query += ` ORDER BY id DESC LIMIT ?`
	args = append(args, limit)

	rows, err := db.read.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("read sync_log: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var entries []SyncLogEntry
	for rows.Next() {
		var (
			e         SyncLogEntry
			ts        string
			nextRetry sql.NullString
			httpS     sql.NullInt64
			rateR     sql.NullInt64
			errStr    sql.NullString
		)
		if err := rows.Scan(&e.ID, &ts, &e.ConnectionID, &e.Operation, &e.Outcome,
			&httpS, &e.ItemsChanged, &rateR, &e.Retries, &nextRetry, &errStr); err != nil {
			return nil, fmt.Errorf("scan sync_log: %w", err)
		}
		if e.Ts, err = parseTime(ts); err != nil {
			return nil, fmt.Errorf("parse sync_log ts: %w", err)
		}
		if httpS.Valid {
			v := int(httpS.Int64)
			e.HTTPStatus = &v
		}
		if rateR.Valid {
			v := int(rateR.Int64)
			e.RateRemaining = &v
		}
		if nextRetry.Valid {
			t, err := parseTime(nextRetry.String)
			if err != nil {
				return nil, fmt.Errorf("parse sync_log next_retry: %w", err)
			}
			e.NextRetry = &t
		}
		if errStr.Valid {
			e.Error = &errStr.String
		}
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read sync_log: %w", err)
	}
	return entries, nil
}

// StoredEvent is one row from the events table with its DB metadata.
type StoredEvent struct {
	ID           int64
	ConnectionID string
	ObjectType   string
	NativeID     string
	EventType    string
	OccurredAt   *time.Time
	Actor        string
	DedupeKey    string
	Payload      json.RawMessage
	ObservedAt   time.Time
}

// ReadEvents returns all events for a connection observed on or after `since`.
// Pass time.Time{} to return all events.
func (db *DB) ReadEvents(ctx context.Context, connectionID string, since time.Time) ([]StoredEvent, error) {
	query := `SELECT id, connection_id, object_type, native_id, event_type,
		occurred_at, actor, dedupe_key, payload, observed_at
		FROM events WHERE connection_id = ?`
	args := []any{connectionID}
	if !since.IsZero() {
		query += ` AND observed_at >= ?`
		args = append(args, since.UTC().Format(timeFormat))
	}
	query += ` ORDER BY id ASC`

	rows, err := db.read.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("read events: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var events []StoredEvent
	for rows.Next() {
		var (
			e          StoredEvent
			occurredAt sql.NullString
			actor      sql.NullString
			payload    string
			observedAt string
		)
		if err := rows.Scan(&e.ID, &e.ConnectionID, &e.ObjectType, &e.NativeID,
			&e.EventType, &occurredAt, &actor, &e.DedupeKey, &payload, &observedAt); err != nil {
			return nil, fmt.Errorf("scan event: %w", err)
		}
		if occurredAt.Valid {
			t, err := parseTime(occurredAt.String)
			if err != nil {
				return nil, fmt.Errorf("parse event occurred_at: %w", err)
			}
			e.OccurredAt = &t
		}
		e.Actor = actor.String
		e.Payload = json.RawMessage(payload)
		if e.ObservedAt, err = parseTime(observedAt); err != nil {
			return nil, fmt.Errorf("parse event observed_at: %w", err)
		}
		events = append(events, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read events: %w", err)
	}
	return events, nil
}

// SetHandleNext sets or clears the "handle next" flag for an item.
func (db *DB) SetHandleNext(ctx context.Context, itemID string, flagged bool) error {
	if !flagged {
		if _, err := db.write.ExecContext(ctx,
			`DELETE FROM handle_next WHERE item_id = ?`, itemID); err != nil {
			return fmt.Errorf("clear handle_next: %w", err)
		}
		return nil
	}
	// Keep the original flagged_at on re-flag so pin ordering is stable.
	if _, err := db.write.ExecContext(ctx,
		`INSERT OR IGNORE INTO handle_next (item_id, flagged_at) VALUES (?, ?)`,
		itemID, time.Now().UTC().Format(timeFormat)); err != nil {
		return fmt.Errorf("set handle_next: %w", err)
	}
	return nil
}

// ListHandleNext returns all flagged item IDs, ordered by flagged_at ascending.
func (db *DB) ListHandleNext(ctx context.Context) ([]string, error) {
	rows, err := db.read.QueryContext(ctx,
		`SELECT item_id FROM handle_next ORDER BY flagged_at ASC, item_id ASC`)
	if err != nil {
		return nil, fmt.Errorf("list handle_next: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan handle_next: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list handle_next: %w", err)
	}
	return ids, nil
}

// migrate brings the database schema up to the latest version by applying any
// pending entries in migrations, one transaction each, and bumping
// schema_version. It is idempotent: already-applied migrations are skipped.
func (db *DB) migrate(ctx context.Context) error {
	if _, err := db.write.ExecContext(ctx,
		`CREATE TABLE IF NOT EXISTS schema_version (version INTEGER NOT NULL);`); err != nil {
		return fmt.Errorf("create schema_version: %w", err)
	}

	var current int
	err := db.write.QueryRowContext(ctx, `SELECT version FROM schema_version LIMIT 1`).Scan(&current)
	if err == sql.ErrNoRows {
		if _, err := db.write.ExecContext(ctx, `INSERT INTO schema_version (version) VALUES (0)`); err != nil {
			return fmt.Errorf("seed schema_version: %w", err)
		}
		current = 0
	} else if err != nil {
		return fmt.Errorf("read schema_version: %w", err)
	}

	for i := current; i < len(migrations); i++ {
		tx, err := db.write.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("migration %d begin: %w", i+1, err)
		}
		if _, err := tx.ExecContext(ctx, migrations[i]); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("migration %d: %w", i+1, err)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE schema_version SET version = ?`, i+1); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("migration %d bump version: %w", i+1, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("migration %d commit: %w", i+1, err)
		}
	}
	return nil
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
