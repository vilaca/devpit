package jira

import (
	"context"
	"log"
	"time"

	"github.com/vilaca/devpit/internal/storage"
)

// refreshEvery is the Jira staleness budget — every sweep fetches every
// referenced ticket unconditionally (ADR-0004: cadence is an engine constant,
// not config; see ADR-0021 amendment for the change from 15 min).
const refreshEvery = 5 * time.Minute

// Notifier is satisfied by *api.Server; the refresher calls AttentionChanged
// after each sweep so connected dashboards re-fetch the attention list.
type Notifier interface {
	AttentionChanged()
}

// Refresher keeps the jira_tickets cache current. It runs one goroutine that
// wakes on the cadence, collects the union of ticket_keys across open items,
// fetches keys that are stale or absent, upserts results, prunes orphaned rows,
// and notifies the SSE hub.
type Refresher struct {
	client   *Client
	db       *storage.DB
	notifier Notifier
}

// NewRefresher constructs a Refresher for the given config, storage, and notifier.
func NewRefresher(cfg Config, db *storage.DB, notifier Notifier) *Refresher {
	return &Refresher{
		client:   NewClient(cfg),
		db:       db,
		notifier: notifier,
	}
}

// Start launches the refresh loop in a new goroutine and returns immediately.
// It runs until ctx is cancelled.
func (r *Refresher) Start(ctx context.Context) {
	go r.loop(ctx)
}

func (r *Refresher) loop(ctx context.Context) {
	r.sweep(ctx)

	ticker := time.NewTicker(refreshEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.sweep(ctx)
		}
	}
}

func (r *Refresher) sweep(ctx context.Context) {
	keys, err := r.db.AllOpenTicketKeys(ctx)
	if err != nil {
		log.Printf("devpit: jira refresher: collect keys: %v", err)
		return
	}

	if len(keys) == 0 {
		if err := r.db.PruneJiraTickets(ctx, nil); err != nil {
			log.Printf("devpit: jira refresher: prune: %v", err)
		}
		return
	}

	changed := false
	for _, key := range keys {
		result, fetchErr, err := r.client.Fetch(ctx, key)
		if err != nil {
			log.Printf("devpit: jira refresher: fetch %s: %v", key, err)
			continue
		}

		now := time.Now().UTC()
		t := storage.JiraTicket{Key: key, FetchedAt: now}
		if fetchErr != "" {
			t.FetchError = &fetchErr
		} else {
			t.Status = result.Status
			t.Summary = result.Summary
			t.Assignee = result.Assignee
			t.URL = result.URL
		}
		if err := r.db.UpsertJiraTicket(ctx, t); err != nil {
			log.Printf("devpit: jira refresher: upsert %s: %v", key, err)
			continue
		}
		if fetchErr == "" {
			changed = true
		}
	}

	if err := r.db.PruneJiraTickets(ctx, keys); err != nil {
		log.Printf("devpit: jira refresher: prune: %v", err)
	}

	if changed {
		r.notifier.AttentionChanged()
	}
}
