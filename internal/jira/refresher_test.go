package jira

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/vilaca/devpit/internal/storage"
	"github.com/vilaca/devpit/sdk"
)

type fakeNotifier struct{ called int }

func (f *fakeNotifier) AttentionChanged() { f.called++ }

func openTestDB(t *testing.T) *storage.DB {
	t.Helper()
	db, err := storage.Open(":memory:")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func seedOpenItem(t *testing.T, db *storage.DB, nativeID string, keys []string) {
	t.Helper()
	_, err := db.WriteEvents(context.Background(), "conn1", []sdk.Event{{
		ObjectType: "merge_request", NativeID: nativeID,
		EventType: "item.observed", DedupeKey: "k-" + nativeID,
		Payload: sdk.ItemObservedPayload{State: "open", Title: "PR " + nativeID, TicketKeys: keys},
	}})
	if err != nil {
		t.Fatalf("WriteEvents: %v", err)
	}
}

func TestRefresherFetchesAndUpserts(t *testing.T) {
	db := openTestDB(t)
	seedOpenItem(t, db, "api#1", []string{"RPC-1"})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"fields":{"summary":"Fix","status":{"name":"In Progress"},"assignee":null}}`))
	}))
	defer srv.Close()

	n := &fakeNotifier{}
	r := NewRefresher(Config{BaseURL: srv.URL, Email: "u@e.com", APIToken: "t"}, db, n)
	r.sweep(context.Background())

	got, err := db.GetJiraTickets(context.Background(), []string{"RPC-1"})
	if err != nil {
		t.Fatalf("GetJiraTickets: %v", err)
	}
	row, ok := got["RPC-1"]
	if !ok {
		t.Fatal("RPC-1 not found after sweep")
	}
	if row.Status != "In Progress" {
		t.Errorf("Status = %q, want In Progress", row.Status)
	}
	if n.called == 0 {
		t.Error("AttentionChanged should have been called after successful fetch")
	}
}

func TestRefresherStaleKeptOnError(t *testing.T) {
	db := openTestDB(t)
	seedOpenItem(t, db, "api#2", []string{"RPC-1"})

	// Seed a good row so the stale-beats-blank logic has something to keep.
	now := time.Now() // freshness is irrelevant; every sweep re-fetches regardless
	goodErr := (*string)(nil)
	if err := db.UpsertJiraTicket(context.Background(), storage.JiraTicket{
		Key: "RPC-1", Status: "Done", FetchedAt: now, FetchError: goodErr,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	n := &fakeNotifier{}
	r := NewRefresher(Config{BaseURL: srv.URL, Email: "u@e.com", APIToken: "t"}, db, n)
	r.sweep(context.Background())

	got, err := db.GetJiraTickets(context.Background(), []string{"RPC-1"})
	if err != nil {
		t.Fatalf("GetJiraTickets: %v", err)
	}
	row := got["RPC-1"]
	if row.Status != "Done" {
		t.Errorf("Status = %q, want Done (stale kept on error)", row.Status)
	}
	if row.FetchError == nil {
		t.Error("FetchError should be set after 500")
	}
	if n.called != 0 {
		t.Error("AttentionChanged should NOT be called when fetch errored")
	}
}

func TestRefresherPrunesOrphanedKeys(t *testing.T) {
	db := openTestDB(t)
	// Only RPC-1 is referenced by open items; RPC-OLD is an orphan.
	seedOpenItem(t, db, "api#3", []string{"RPC-1"})
	now := time.Now()
	if err := db.UpsertJiraTicket(context.Background(), storage.JiraTicket{
		Key: "RPC-OLD", Status: "Old", FetchedAt: now,
	}); err != nil {
		t.Fatalf("seed orphan: %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"fields":{"summary":"","status":{"name":"Open"},"assignee":null}}`))
	}))
	defer srv.Close()

	r := NewRefresher(Config{BaseURL: srv.URL, Email: "u@e.com", APIToken: "t"}, db, &fakeNotifier{})
	r.sweep(context.Background())

	got, err := db.GetJiraTickets(context.Background(), []string{"RPC-OLD"})
	if err != nil {
		t.Fatalf("GetJiraTickets: %v", err)
	}
	if _, ok := got["RPC-OLD"]; ok {
		t.Error("RPC-OLD should have been pruned")
	}
}

// TestRefresherRefetchesFreshRow is the regression test for the bug where a
// freshness guard caused every ticket to be skipped on every other sweep,
// yielding an effective ~30 min worst-case staleness.
func TestRefresherRefetchesFreshRow(t *testing.T) {
	db := openTestDB(t)
	seedOpenItem(t, db, "api#4", []string{"RPC-1"})

	// Seed a very fresh row; sweep must still re-fetch it.
	if err := db.UpsertJiraTicket(context.Background(), storage.JiraTicket{
		Key: "RPC-1", Status: "Stale", FetchedAt: time.Now(),
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"fields":{"summary":"Fix","status":{"name":"In Progress"},"assignee":null}}`))
	}))
	defer srv.Close()

	n := &fakeNotifier{}
	r := NewRefresher(Config{BaseURL: srv.URL, Email: "u@e.com", APIToken: "t"}, db, n)
	r.sweep(context.Background())

	if calls == 0 {
		t.Error("HTTP handler should have been hit (fresh row must still be re-fetched)")
	}
	got, err := db.GetJiraTickets(context.Background(), []string{"RPC-1"})
	if err != nil {
		t.Fatalf("GetJiraTickets: %v", err)
	}
	if got["RPC-1"].Status != "In Progress" {
		t.Errorf("Status = %q, want In Progress", got["RPC-1"].Status)
	}
	if n.called == 0 {
		t.Error("AttentionChanged should have fired after successful fetch")
	}
}

func TestRefresherFetchesAllKeysEverySweep(t *testing.T) {
	db := openTestDB(t)
	seedOpenItem(t, db, "api#5", []string{"RPC-1"})
	seedOpenItem(t, db, "api#6", []string{"RPC-2"})

	// Seed fresh rows for both keys.
	for _, key := range []string{"RPC-1", "RPC-2"} {
		if err := db.UpsertJiraTicket(context.Background(), storage.JiraTicket{
			Key: key, Status: "Open", FetchedAt: time.Now(),
		}); err != nil {
			t.Fatalf("seed %s: %v", key, err)
		}
	}

	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"fields":{"summary":"","status":{"name":"Open"},"assignee":null}}`))
	}))
	defer srv.Close()

	r := NewRefresher(Config{BaseURL: srv.URL, Email: "u@e.com", APIToken: "t"}, db, &fakeNotifier{})
	r.sweep(context.Background())
	r.sweep(context.Background())

	if calls != 4 {
		t.Errorf("HTTP calls = %d, want 4 (2 keys × 2 sweeps)", calls)
	}
}

func TestRefresherMixedOutcomeNotifiesOnPartialSuccess(t *testing.T) {
	db := openTestDB(t)
	seedOpenItem(t, db, "api#7", []string{"RPC-1", "RPC-2"})

	// Seed a good prior row for RPC-2 so stale-kept-on-error has something to keep.
	if err := db.UpsertJiraTicket(context.Background(), storage.JiraTicket{
		Key: "RPC-2", Status: "Done", FetchedAt: time.Now(),
	}); err != nil {
		t.Fatalf("seed RPC-2: %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "RPC-2") {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"fields":{"summary":"Fix","status":{"name":"In Progress"},"assignee":null}}`))
	}))
	defer srv.Close()

	n := &fakeNotifier{}
	r := NewRefresher(Config{BaseURL: srv.URL, Email: "u@e.com", APIToken: "t"}, db, n)
	r.sweep(context.Background())

	got, err := db.GetJiraTickets(context.Background(), []string{"RPC-2"})
	if err != nil {
		t.Fatalf("GetJiraTickets: %v", err)
	}
	row := got["RPC-2"]
	if row.Status != "Done" {
		t.Errorf("RPC-2 Status = %q, want Done (stale kept on error)", row.Status)
	}
	if row.FetchError == nil {
		t.Error("RPC-2 FetchError should be set after 500")
	}
	if n.called == 0 {
		t.Error("AttentionChanged should fire when at least one fetch succeeds")
	}
}

// TestRefresherCollectKeysErrorSkipsSweep exercises AllOpenTicketKeys's error
// path: closing the DB is the real failure surface (a query against a closed
// *sql.DB), not a mock. The sweep must log and return without touching the
// HTTP client or notifier.
func TestRefresherCollectKeysErrorSkipsSweep(t *testing.T) {
	db := openTestDB(t)
	if err := db.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Error("HTTP handler should not be called when key collection fails")
	}))
	defer srv.Close()

	n := &fakeNotifier{}
	r := NewRefresher(Config{BaseURL: srv.URL, Email: "u@e.com", APIToken: "t"}, db, n)
	r.sweep(context.Background()) // must not panic despite the closed DB

	if n.called != 0 {
		t.Error("AttentionChanged should not be called when key collection fails")
	}
}

// TestRefresherLoopExitsOnContextCancel drives loop directly (the function
// Start launches): the initial sweep runs immediately, then the loop must
// return once ctx is cancelled rather than blocking on the ticker forever.
func TestRefresherLoopExitsOnContextCancel(t *testing.T) {
	db := openTestDB(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"fields":{"summary":"","status":{"name":"Open"},"assignee":null}}`))
	}))
	defer srv.Close()

	r := NewRefresher(Config{BaseURL: srv.URL, Email: "u@e.com", APIToken: "t"}, db, &fakeNotifier{})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		r.loop(ctx)
		close(done)
	}()
	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("loop did not exit after context cancellation")
	}
}

func TestRefresherNoKeysNoNotify(t *testing.T) {
	db := openTestDB(t)
	// No open items — keys is empty.

	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Error("HTTP handler should not be called with no keys")
	}))
	defer srv.Close()

	n := &fakeNotifier{}
	r := NewRefresher(Config{BaseURL: srv.URL, Email: "u@e.com", APIToken: "t"}, db, n)
	r.sweep(context.Background())

	if n.called != 0 {
		t.Error("AttentionChanged should not be called with no keys")
	}
}
