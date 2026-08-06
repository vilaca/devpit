package storage

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

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
	args := make([]any, len(keys))
	for i, k := range keys {
		args[i] = k
	}
	//nolint:gosec // inClause builds "?" literals, not user input
	rows, err := db.read.QueryContext(ctx,
		`SELECT key, status, summary, assignee, url, fetched_at, fetch_error
		 FROM jira_tickets WHERE key IN (`+inClause(len(keys))+`)`, args...)
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
	args := make([]any, len(keep))
	for i, k := range keep {
		args[i] = k
	}
	//nolint:gosec // inClause builds "?" literals, not user input
	if _, err := db.write.ExecContext(ctx,
		`DELETE FROM jira_tickets WHERE key NOT IN (`+inClause(len(keep))+`)`, args...); err != nil {
		return fmt.Errorf("prune jira tickets: %w", err)
	}
	return nil
}
