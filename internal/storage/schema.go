package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// migrations are applied in order at Open time. Each entry's index+1 is its
// version; schema_version stores the highest applied version. Pre-1.0 the whole
// schema is a single migration (version 1); post-release, every schema change
// appends a new entry rather than editing this one.
var migrations = []string{
	`CREATE TABLE events (
		id            INTEGER PRIMARY KEY,
		connection_id TEXT NOT NULL,
		object_type   TEXT NOT NULL,
		native_id     TEXT NOT NULL,
		event_type    TEXT NOT NULL,
		occurred_at   TEXT,
		actor         TEXT,
		dedupe_key    TEXT NOT NULL,
		payload       TEXT NOT NULL,
		observed_at   TEXT NOT NULL,
		UNIQUE (connection_id, object_type, native_id, event_type, dedupe_key)
	);
	CREATE INDEX events_by_item ON events
		(connection_id, object_type, native_id, id);

	CREATE TABLE sync_cursors (
		connection_id TEXT NOT NULL,
		key           TEXT NOT NULL,
		value         TEXT NOT NULL,
		PRIMARY KEY (connection_id, key)
	);

	CREATE TABLE sync_log (
		id             INTEGER PRIMARY KEY,
		ts             TEXT NOT NULL,
		connection_id  TEXT NOT NULL,
		operation      TEXT NOT NULL,
		outcome        TEXT NOT NULL,
		http_status    INTEGER,
		items_changed  INTEGER NOT NULL DEFAULT 0,
		rate_remaining INTEGER,
		retries        INTEGER NOT NULL DEFAULT 0,
		next_retry     TEXT,
		error          TEXT
	);
	CREATE INDEX sync_log_by_conn ON sync_log (connection_id, ts);

	CREATE TABLE handle_next (
		item_id    TEXT PRIMARY KEY,
		flagged_at TEXT NOT NULL
	);

	CREATE TABLE jira_tickets (
		key         TEXT PRIMARY KEY,
		status      TEXT NOT NULL DEFAULT '',
		summary     TEXT NOT NULL DEFAULT '',
		assignee    TEXT NOT NULL DEFAULT '',
		url         TEXT NOT NULL DEFAULT '',
		fetched_at  TEXT NOT NULL,
		fetch_error TEXT
	);

	CREATE TABLE repo_approvers (
		connection_id    TEXT NOT NULL,
		repo             TEXT NOT NULL,
		is_sole_approver INTEGER NOT NULL,
		fetched_at       TEXT NOT NULL,
		PRIMARY KEY (connection_id, repo)
	);`,
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
