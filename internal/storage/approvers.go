package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

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
