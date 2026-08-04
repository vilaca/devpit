package storage

import (
	"context"
	"fmt"
	"time"
)

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
