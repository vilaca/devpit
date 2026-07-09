package storage

// migrations are applied in order at Open time. Each entry's index+1 is its
// version; schema_version stores the highest applied version.
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
	);`,
}
