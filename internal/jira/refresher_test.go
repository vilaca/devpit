package jira

import (
	"context"
	"net/http"
	"net/http/httptest"
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
	now := time.Now().Add(-time.Hour) // old enough to trigger re-fetch
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

func TestRefresherSkipsRecentRow(t *testing.T) {
	db := openTestDB(t)
	seedOpenItem(t, db, "api#4", []string{"RPC-1"})

	// Insert a very fresh row — sweep must not re-fetch it.
	if err := db.UpsertJiraTicket(context.Background(), storage.JiraTicket{
		Key: "RPC-1", Status: "Fresh", FetchedAt: time.Now(),
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	r := NewRefresher(Config{BaseURL: srv.URL, Email: "u@e.com", APIToken: "t"}, db, &fakeNotifier{})
	r.sweep(context.Background())

	if calls != 0 {
		t.Errorf("HTTP calls = %d, want 0 (row is fresh)", calls)
	}
}
