package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

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

// LatestOutcomesPerOperation returns the outcome of the most recent sync_log
// row for each distinct operation for connectionID. Returns an empty slice when
// no rows exist. Used by internal/api to compute per-connection health.
func (db *DB) LatestOutcomesPerOperation(ctx context.Context, connectionID string) ([]SyncLogEntry, error) {
	// SQLite guarantees that non-aggregated columns come from the row that
	// holds the MAX value, making outcome deterministic here.
	rows, err := db.read.QueryContext(ctx,
		`SELECT operation, outcome, MAX(id) FROM sync_log WHERE connection_id = ? GROUP BY operation`,
		connectionID)
	if err != nil {
		return nil, fmt.Errorf("latest outcomes per operation: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var entries []SyncLogEntry
	for rows.Next() {
		var e SyncLogEntry
		var maxID int64
		if err := rows.Scan(&e.Operation, &e.Outcome, &maxID); err != nil {
			return nil, fmt.Errorf("scan latest outcomes: %w", err)
		}
		entries = append(entries, e)
	}
	return entries, rows.Err()
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
