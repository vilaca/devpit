package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/vilaca/devpit/sdk"

	// Register the modernc pure-Go sqlite driver for database/sql.
	_ "modernc.org/sqlite"
)

// timeFormat is RFC 3339 UTC, matching the storage schema.
const timeFormat = time.RFC3339

// itemStateOpen is the wire value for an open item; used across storage methods.
const itemStateOpen = "open"

// eventItemObserved is the fact-stream event type for an observed item
// snapshot; the string matches the engine constant. AllOpenTicketKeys folds
// the latest observed/removed fact per item and keeps only those whose latest
// is this type (docs/Event_Taxonomy_and_Storage.md).
const eventItemObserved = "item.observed"

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
	return scanSyncLogRows(rows)
}

// ReadSyncLogSince returns all cycle rows for connectionID with ts >= since,
// newest first. Used by internal/api to compute per-connection health.
func (db *DB) ReadSyncLogSince(ctx context.Context, connectionID string, since time.Time) ([]SyncLogEntry, error) {
	rows, err := db.read.QueryContext(ctx, `
		SELECT id, ts, connection_id, operation, outcome, http_status,
			items_changed, rate_remaining, retries, next_retry, error
		FROM sync_log WHERE connection_id = ? AND ts >= ?
		ORDER BY id DESC`,
		connectionID, since.UTC().Format(timeFormat))
	if err != nil {
		return nil, fmt.Errorf("read sync_log since: %w", err)
	}
	defer func() { _ = rows.Close() }()
	return scanSyncLogRows(rows)
}

// LastSyncedAt returns the timestamp of the most recent successful poll cycle
// for connectionID. Returns a zero Time when no successful cycle exists yet.
// A "degraded" cycle counts as a success: it persisted events and cursors and
// reset backoff (internal/engine/cycle.go), and per ADR-0024 it is the common
// steady state for accounts hitting the GraphQL complexity ceiling — so a
// healthy-but-degraded connection must not report as never-synced. Ordering by
// ts uses the sync_log_by_conn (connection_id, ts) index.
func (db *DB) LastSyncedAt(ctx context.Context, connectionID string) (time.Time, error) {
	var ts string
	err := db.read.QueryRowContext(ctx,
		`SELECT ts FROM sync_log WHERE connection_id = ? AND outcome IN ('ok', 'degraded')
		ORDER BY ts DESC LIMIT 1`,
		connectionID).Scan(&ts)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, nil
	}
	if err != nil {
		return time.Time{}, fmt.Errorf("last synced at: %w", err)
	}
	t, err := parseTime(ts)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse last synced at: %w", err)
	}
	return t, nil
}

// scanSyncLogRows scans all rows from a sync_log SELECT into SyncLogEntry values.
func scanSyncLogRows(rows *sql.Rows) ([]SyncLogEntry, error) {
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
		t, err := parseTime(ts)
		if err != nil {
			return nil, fmt.Errorf("parse sync_log ts: %w", err)
		}
		e.Ts = t
		if httpS.Valid {
			v := int(httpS.Int64)
			e.HTTPStatus = &v
		}
		if rateR.Valid {
			v := int(rateR.Int64)
			e.RateRemaining = &v
		}
		if nextRetry.Valid {
			nt, err := parseTime(nextRetry.String)
			if err != nil {
				return nil, fmt.Errorf("parse sync_log next_retry: %w", err)
			}
			e.NextRetry = &nt
		}
		if errStr.Valid {
			e.Error = &errStr.String
		}
		entries = append(entries, e)
	}
	return entries, rows.Err()
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
//
// The itemID is intentionally NOT validated against a live item: liveness is
// derived from the event fold (there is no items table to cheaply check), and
// coupling this write to the read model isn't worth it for a single-user,
// self-hosted app. A flag for a non-existent id is inert — ListHandleNext pins
// only surface when they match a listed item — so at worst a misbehaving client
// leaves harmless orphan rows (ADR-0017).
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

// PinnedItem is a flagged item from the handle_next table.
type PinnedItem struct {
	ID        string
	FlaggedAt time.Time
}

// ListHandleNext returns all flagged items with their flagged_at timestamps,
// ordered by flagged_at ascending.
func (db *DB) ListHandleNext(ctx context.Context) ([]PinnedItem, error) {
	rows, err := db.read.QueryContext(ctx,
		`SELECT item_id, flagged_at FROM handle_next ORDER BY flagged_at ASC, item_id ASC`)
	if err != nil {
		return nil, fmt.Errorf("list handle_next: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var items []PinnedItem
	for rows.Next() {
		var id, flaggedAtStr string
		if err := rows.Scan(&id, &flaggedAtStr); err != nil {
			return nil, fmt.Errorf("scan handle_next: %w", err)
		}
		flaggedAt, err := parseTime(flaggedAtStr)
		if err != nil {
			return nil, fmt.Errorf("parse handle_next flagged_at: %w", err)
		}
		items = append(items, PinnedItem{ID: id, FlaggedAt: flaggedAt})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list handle_next: %w", err)
	}
	return items, nil
}

// JiraTicket is one row of the jira_tickets cache table.
type JiraTicket struct {
	Key        string
	Status     string
	Summary    string
	Assignee   string
	URL        string
	FetchedAt  time.Time
	FetchError *string
}

// UpsertJiraTicket inserts or replaces a jira_tickets row. A failed fetch
// keeps the previous status/summary/assignee/url (stale beats blank) by only
// updating fetched_at and fetch_error when FetchError is non-nil and the row
// already exists with meaningful data.
func (db *DB) UpsertJiraTicket(ctx context.Context, t JiraTicket) error {
	_, err := db.write.ExecContext(ctx, `
		INSERT INTO jira_tickets (key, status, summary, assignee, url, fetched_at, fetch_error)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (key) DO UPDATE SET
			status      = CASE WHEN excluded.fetch_error IS NULL THEN excluded.status      ELSE status      END,
			summary     = CASE WHEN excluded.fetch_error IS NULL THEN excluded.summary     ELSE summary     END,
			assignee    = CASE WHEN excluded.fetch_error IS NULL THEN excluded.assignee    ELSE assignee    END,
			url         = CASE WHEN excluded.fetch_error IS NULL THEN excluded.url         ELSE url         END,
			fetched_at  = excluded.fetched_at,
			fetch_error = excluded.fetch_error`,
		t.Key, t.Status, t.Summary, t.Assignee, t.URL,
		t.FetchedAt.UTC().Format(timeFormat), nullStr(t.FetchError))
	if err != nil {
		return fmt.Errorf("upsert jira ticket %q: %w", t.Key, err)
	}
	return nil
}

// GetJiraTickets returns the cached jira_tickets rows for the given keys,
// keyed by ticket key. Missing keys are simply absent from the map.
func (db *DB) GetJiraTickets(ctx context.Context, keys []string) (map[string]JiraTicket, error) {
	if len(keys) == 0 {
		return map[string]JiraTicket{}, nil
	}
	placeholders := strings.Repeat("?,", len(keys))
	placeholders = placeholders[:len(placeholders)-1]
	args := make([]any, len(keys))
	for i, k := range keys {
		args[i] = k
	}
	//nolint:gosec // placeholders is built from len(keys) "?" literals, not user input
	rows, err := db.read.QueryContext(ctx,
		`SELECT key, status, summary, assignee, url, fetched_at, fetch_error
		 FROM jira_tickets WHERE key IN (`+placeholders+`)`, args...)
	if err != nil {
		return nil, fmt.Errorf("get jira tickets: %w", err)
	}
	defer func() { _ = rows.Close() }()

	result := make(map[string]JiraTicket, len(keys))
	for rows.Next() {
		var (
			t         JiraTicket
			fetchedAt string
			fetchErr  sql.NullString
		)
		if err := rows.Scan(&t.Key, &t.Status, &t.Summary, &t.Assignee, &t.URL, &fetchedAt, &fetchErr); err != nil {
			return nil, fmt.Errorf("scan jira ticket: %w", err)
		}
		if t.FetchedAt, err = parseTime(fetchedAt); err != nil {
			return nil, fmt.Errorf("parse jira ticket fetched_at: %w", err)
		}
		if fetchErr.Valid {
			t.FetchError = &fetchErr.String
		}
		result[t.Key] = t
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("get jira tickets: %w", err)
	}
	return result, nil
}

// PruneJiraTickets deletes rows whose key is not in keep. Pass an empty/nil
// keep slice to delete all rows.
func (db *DB) PruneJiraTickets(ctx context.Context, keep []string) error {
	if len(keep) == 0 {
		if _, err := db.write.ExecContext(ctx, `DELETE FROM jira_tickets`); err != nil {
			return fmt.Errorf("prune jira tickets: %w", err)
		}
		return nil
	}
	placeholders := strings.Repeat("?,", len(keep))
	placeholders = placeholders[:len(placeholders)-1]
	args := make([]any, len(keep))
	for i, k := range keep {
		args[i] = k
	}
	//nolint:gosec // placeholders is built from len(keep) "?" literals, not user input
	if _, err := db.write.ExecContext(ctx,
		`DELETE FROM jira_tickets WHERE key NOT IN (`+placeholders+`)`, args...); err != nil {
		return fmt.Errorf("prune jira tickets: %w", err)
	}
	return nil
}

// AllOpenTicketKeys returns the union of ticket_keys across every item whose
// latest fact is an item.observed snapshot in the "open" state. Used by the
// Jira refresher to decide which keys to fetch and which rows to prune.
//
// The latest fact is taken across BOTH item.observed and item.removed (the
// same max(id) fold LatestItemFacts uses), so an item the engine has reaped —
// whose latest fact is an item.removed — is excluded even though its last
// observed snapshot still reads state="open". Scoping the fold to
// item.observed alone would keep returning reaped items' keys forever, so the
// jira_tickets rows would never be pruned (ADR-0024, ADR-0021).
func (db *DB) AllOpenTicketKeys(ctx context.Context) ([]string, error) {
	rows, err := db.read.QueryContext(ctx, `
		SELECT e.event_type, e.payload
		FROM events e
		JOIN (
			SELECT connection_id, object_type, native_id, max(id) AS id
			FROM events
			WHERE event_type IN ('item.observed', 'item.removed')
			GROUP BY connection_id, object_type, native_id
		) latest ON e.id = latest.id`)
	if err != nil {
		return nil, fmt.Errorf("all open ticket keys: %w", err)
	}
	defer func() { _ = rows.Close() }()

	seen := map[string]bool{}
	var keys []string
	for rows.Next() {
		var eventType, payload string
		if err := rows.Scan(&eventType, &payload); err != nil {
			return nil, fmt.Errorf("scan payload: %w", err)
		}
		if eventType != eventItemObserved {
			continue // latest fact is a removal — the item is gone, skip its keys
		}
		var p struct {
			State      string   `json:"state"`
			TicketKeys []string `json:"ticket_keys"`
		}
		if err := json.Unmarshal([]byte(payload), &p); err != nil || p.State != itemStateOpen {
			continue
		}
		for _, k := range p.TicketKeys {
			if !seen[k] {
				seen[k] = true
				keys = append(keys, k)
			}
		}
	}
	return keys, rows.Err()
}

// ItemFact is one item's latest fact-stream event — the max-id row among the
// item.observed / item.removed events for its (object_type, native_id). Signals
// are ignored, so this mirrors the fold's notion of an item's current state
// (docs/Attention_Engine.md): an ItemFact whose EventType is item.observed with
// an open payload is a live item, and one whose EventType is item.removed is a
// reaped item. The engine folds these to reap items that left the reconcile
// sweep and to salt resurrection (ADR-0024).
type ItemFact struct {
	ObjectType string
	NativeID   string
	EventID    int64
	EventType  string
	Payload    json.RawMessage
}

// LatestItemFacts returns, for connectionID, the latest fact-stream event of
// every item that has one — one ItemFact per (object_type, native_id), being
// its max-id item.observed or item.removed event. The latest-per-item shape
// mirrors AllOpenTicketKeys' max(id) GROUP BY pattern.
func (db *DB) LatestItemFacts(ctx context.Context, connectionID string) ([]ItemFact, error) {
	rows, err := db.read.QueryContext(ctx, `
		SELECT e.id, e.object_type, e.native_id, e.event_type, e.payload
		FROM events e
		JOIN (
			SELECT object_type, native_id, max(id) AS id
			FROM events
			WHERE connection_id = ? AND event_type IN ('item.observed', 'item.removed')
			GROUP BY object_type, native_id
		) latest ON e.id = latest.id`, connectionID)
	if err != nil {
		return nil, fmt.Errorf("latest item facts: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var facts []ItemFact
	for rows.Next() {
		var (
			f       ItemFact
			payload string
		)
		if err := rows.Scan(&f.EventID, &f.ObjectType, &f.NativeID, &f.EventType, &payload); err != nil {
			return nil, fmt.Errorf("scan item fact: %w", err)
		}
		f.Payload = json.RawMessage(payload)
		facts = append(facts, f)
	}
	return facts, rows.Err()
}

// RepoApprover is one row of the repo_approvers table.
type RepoApprover struct {
	ConnectionID   string
	Repo           string
	IsSoleApprover bool
	FetchedAt      time.Time
}

// UpsertRepoApprover inserts or replaces a repo_approvers row.
func (db *DB) UpsertRepoApprover(ctx context.Context, r RepoApprover) error {
	var isSole int
	if r.IsSoleApprover {
		isSole = 1
	}
	_, err := db.write.ExecContext(ctx, `
		INSERT OR REPLACE INTO repo_approvers (connection_id, repo, is_sole_approver, fetched_at)
		VALUES (?, ?, ?, ?)`,
		r.ConnectionID, r.Repo, isSole, r.FetchedAt.UTC().Format(timeFormat))
	if err != nil {
		return fmt.Errorf("upsert repo_approver (%q, %q): %w", r.ConnectionID, r.Repo, err)
	}
	return nil
}

// GetRepoApprover returns the cached repo_approvers row for the given
// connection and repo. Returns (RepoApprover{}, false, nil) when no row exists.
func (db *DB) GetRepoApprover(ctx context.Context, connectionID, repo string) (RepoApprover, bool, error) {
	var (
		r         RepoApprover
		isSole    int
		fetchedAt string
	)
	err := db.read.QueryRowContext(ctx, `
		SELECT connection_id, repo, is_sole_approver, fetched_at
		FROM repo_approvers WHERE connection_id = ? AND repo = ?`,
		connectionID, repo).Scan(&r.ConnectionID, &r.Repo, &isSole, &fetchedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return RepoApprover{}, false, nil
	}
	if err != nil {
		return RepoApprover{}, false, fmt.Errorf("get repo_approver (%q, %q): %w", connectionID, repo, err)
	}
	r.IsSoleApprover = isSole != 0
	t, err := parseTime(fetchedAt)
	if err != nil {
		return RepoApprover{}, false, fmt.Errorf("parse repo_approver fetched_at: %w", err)
	}
	r.FetchedAt = t
	return r, true, nil
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
	if errors.Is(err, sql.ErrNoRows) {
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
		// Scope the bump to the row we just read (single-row invariant): a WHERE
		// stops a stray second row from being clobbered to the same version.
		if _, err := tx.ExecContext(ctx, `UPDATE schema_version SET version = ? WHERE version = ?`, i+1, i); err != nil {
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
