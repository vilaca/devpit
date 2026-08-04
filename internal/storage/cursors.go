package storage

import (
	"context"
	"fmt"

	"github.com/vilaca/devpit/sdk"
)

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
