package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/vilaca/devpit/sdk"
)

// itemStateOpen is the wire value for an open item; used across storage methods.
const itemStateOpen = "open"

// eventItemObserved is the fact-stream event type for an observed item
// snapshot. AllOpenTicketKeys folds the latest observed/removed fact per item
// and keeps only those whose latest is this type (docs/Event_Taxonomy_and_Storage.md).
const eventItemObserved = sdk.EventItemObserved

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
