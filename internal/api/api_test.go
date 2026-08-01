package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/vilaca/devpit/internal/attention"
	"github.com/vilaca/devpit/internal/storage"
	"github.com/vilaca/devpit/sdk"
)

// testMeta is a fixed set of ConnectionMeta used across tests.
var testMeta = []ConnectionMeta{
	{ID: "gh", Type: "github", BaseURL: "https://github.com", Label: "Personal", Identity: "jdoe"},
}

func openTestDB(t *testing.T) *storage.DB {
	t.Helper()
	db, err := storage.Open(":memory:")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func newTestServer(t *testing.T, db *storage.DB) *Server {
	t.Helper()
	return New(db, testMeta, attention.DefaultStaleThreshold, attention.DefaultOldThreshold)
}

func do(t *testing.T, s *Server, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequestWithContext(context.Background(), method, path, nil)
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	return w
}

// writeTestEvent inserts an item.observed event for connection "gh".
func writeTestEvent(t *testing.T, db *storage.DB, nativeID string, facts sdk.ItemObservedPayload) {
	t.Helper()
	events := []sdk.Event{{
		ObjectType: "merge_request",
		NativeID:   nativeID,
		EventType:  "item.observed",
		DedupeKey:  nativeID,
		Payload:    facts,
	}}
	if _, err := db.WriteEvents(context.Background(), "gh", events); err != nil {
		t.Fatalf("WriteEvents: %v", err)
	}
}

func openFacts(roles []string, reviewState, gate string) sdk.ItemObservedPayload {
	return sdk.ItemObservedPayload{
		Title:             "Fix thing",
		URL:               "https://github.com/acme/api/pull/1",
		Repo:              "acme/api",
		State:             "open",
		Author:            "jdoe",
		MyRoles:           roles,
		MyReviewState:     reviewState,
		Gate:              gate,
		ProviderUpdatedAt: time.Now().UTC().Format(time.RFC3339),
	}
}

// --- GET /attention ---

func TestAttentionEmpty(t *testing.T) {
	s := newTestServer(t, openTestDB(t))
	w := do(t, s, "GET", "/attention")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var resp attentionResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Items == nil || len(resp.Items) != 0 {
		t.Errorf("want empty items array, got %v", resp.Items)
	}
}

func TestAttentionReturnsItem(t *testing.T) {
	db := openTestDB(t)
	writeTestEvent(t, db, "acme/api#1", openFacts([]string{"reviewer"}, "requested", ""))
	s := newTestServer(t, db)

	w := do(t, s, "GET", "/attention")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var resp attentionResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Items) != 1 {
		t.Fatalf("want 1 item, got %d", len(resp.Items))
	}
	item := resp.Items[0]
	if item.ConnectionLabel != "Personal" {
		t.Errorf("connection_label = %q, want Personal", item.ConnectionLabel)
	}
	if item.ConnectionType != "github" {
		t.Errorf("connection_type = %q, want github", item.ConnectionType)
	}
	if len(item.States) == 0 || item.States[0] != attention.StateReviewRequested {
		t.Errorf("states = %v, want [review_requested]", item.States)
	}
	if item.NativeID != "acme/api#1" {
		t.Errorf("native_id = %q, want acme/api#1", item.NativeID)
	}
}

func TestAttentionStateFilter(t *testing.T) {
	db := openTestDB(t)
	writeTestEvent(t, db, "acme/api#1", openFacts([]string{"reviewer"}, "requested", ""))
	writeTestEvent(t, db, "acme/api#2", openFacts([]string{"author"}, "", "ready"))
	s := newTestServer(t, db)

	w := do(t, s, "GET", "/attention?state=ready_to_merge")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var resp attentionResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Items) != 1 {
		t.Fatalf("want 1 item after filter, got %d", len(resp.Items))
	}
	if resp.Items[0].NativeID != "acme/api#2" {
		t.Errorf("filtered item native_id = %q, want acme/api#2", resp.Items[0].NativeID)
	}
}

func TestAttentionUnknownStateFilterReturnsEmpty(t *testing.T) {
	db := openTestDB(t)
	writeTestEvent(t, db, "acme/api#1", openFacts([]string{"reviewer"}, "requested", ""))
	s := newTestServer(t, db)

	w := do(t, s, "GET", "/attention?state=nonexistent")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var resp attentionResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Items) != 0 {
		t.Errorf("unknown state filter should return empty, got %d items", len(resp.Items))
	}
}

func TestAttentionJiraDecoration(t *testing.T) {
	db := openTestDB(t)
	facts := openFacts([]string{"author"}, "", "ready")
	facts.TicketKeys = []string{"RPC-1", "RPC-2"}
	writeTestEvent(t, db, "acme/api#1", facts)

	// Seed RPC-1 with status, RPC-2 without.
	now := time.Now()
	if err := db.UpsertJiraTicket(context.Background(), storage.JiraTicket{
		Key: "RPC-1", Status: "In Review", URL: "https://example.atlassian.net/browse/RPC-1", FetchedAt: now,
	}); err != nil {
		t.Fatalf("UpsertJiraTicket: %v", err)
	}

	s := newTestServer(t, db)
	w := do(t, s, "GET", "/attention")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var resp attentionResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Items) != 1 {
		t.Fatalf("items = %d, want 1", len(resp.Items))
	}
	item := resp.Items[0]
	if item.Jira == nil {
		t.Fatal("Jira should be set when ticket has status")
	}
	if item.Jira.Key != "RPC-1" {
		t.Errorf("Jira.Key = %q, want RPC-1", item.Jira.Key)
	}
	if item.Jira.Status != "In Review" {
		t.Errorf("Jira.Status = %q, want In Review", item.Jira.Status)
	}
}

func TestAttentionJiraAbsentWhenNoStatus(t *testing.T) {
	db := openTestDB(t)
	facts := openFacts([]string{"author"}, "", "ready")
	facts.TicketKeys = []string{"RPC-99"}
	writeTestEvent(t, db, "acme/api#2", facts)

	// No row in jira_tickets for RPC-99.
	s := newTestServer(t, db)
	w := do(t, s, "GET", "/attention")
	var resp attentionResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Items) != 1 {
		t.Fatalf("items = %d, want 1", len(resp.Items))
	}
	if resp.Items[0].Jira != nil {
		t.Errorf("Jira = %+v, want nil when no row", resp.Items[0].Jira)
	}
}

func TestAttentionJiraFirstKeyWithData(t *testing.T) {
	db := openTestDB(t)
	facts := openFacts([]string{"author"}, "", "ready")
	facts.TicketKeys = []string{"NO-STATUS", "HAS-STATUS"}
	writeTestEvent(t, db, "acme/api#3", facts)

	now := time.Now()
	// NO-STATUS has an empty status (row exists but blank).
	if err := db.UpsertJiraTicket(context.Background(), storage.JiraTicket{
		Key: "NO-STATUS", Status: "", FetchedAt: now,
	}); err != nil {
		t.Fatalf("UpsertJiraTicket: %v", err)
	}
	if err := db.UpsertJiraTicket(context.Background(), storage.JiraTicket{
		Key: "HAS-STATUS", Status: "Done", URL: "https://x/browse/HAS-STATUS", FetchedAt: now,
	}); err != nil {
		t.Fatalf("UpsertJiraTicket: %v", err)
	}

	s := newTestServer(t, db)
	w := do(t, s, "GET", "/attention")
	var resp attentionResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Items[0].Jira == nil || resp.Items[0].Jira.Key != "HAS-STATUS" {
		t.Errorf("Jira = %+v, want key=HAS-STATUS (first with non-empty status)", resp.Items[0].Jira)
	}
}

// --- GET /connections ---

func TestConnectionsReturnsAll(t *testing.T) {
	s := newTestServer(t, openTestDB(t))
	w := do(t, s, "GET", "/connections")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var resp connectionsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Connections) != 1 {
		t.Fatalf("want 1 connection, got %d", len(resp.Connections))
	}
	c := resp.Connections[0]
	if c.ID != "gh" || c.Type != "github" || c.Label != "Personal" {
		t.Errorf("connection fields wrong: %+v", c)
	}
	if c.Identity == nil || *c.Identity != "jdoe" {
		t.Errorf("identity = %v, want jdoe", c.Identity)
	}
}

func TestConnectionsIdentityNullWhenEmpty(t *testing.T) {
	meta := []ConnectionMeta{{ID: "gh", Type: "github", Label: "Personal"}} // no Identity
	s := New(openTestDB(t), meta, attention.DefaultStaleThreshold, attention.DefaultOldThreshold)
	w := do(t, s, "GET", "/connections")
	var resp connectionsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Connections[0].Identity != nil {
		t.Errorf("identity = %v, want null", resp.Connections[0].Identity)
	}
}

func TestConnectionsHealthOK(t *testing.T) {
	db := openTestDB(t)
	_ = db.WriteSyncLog(context.Background(), storage.SyncLogEntry{
		Ts: time.Now(), ConnectionID: "gh", Operation: "fast_poll", Outcome: "ok",
	})
	s := newTestServer(t, db)
	w := do(t, s, "GET", "/connections")
	var resp connectionsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	h := resp.Connections[0].Health
	if h.Status != healthOK {
		t.Errorf("status = %q, want ok", h.Status)
	}
	if h.FailureCount != 0 {
		t.Errorf("failure_count = %d, want 0", h.FailureCount)
	}
	if h.LastSyncedAt == nil {
		t.Error("last_synced_at should be non-null after a successful sync")
	}
	if h.FailureWindowMinutes != failureWindowMinutes {
		t.Errorf("failure_window_minutes = %d, want %d", h.FailureWindowMinutes, failureWindowMinutes)
	}
}

func TestConnectionsHealthDegraded(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	_ = db.WriteSyncLog(ctx, storage.SyncLogEntry{
		Ts: time.Now(), ConnectionID: "gh", Operation: "fast_poll", Outcome: "ok",
	})
	_ = db.WriteSyncLog(ctx, storage.SyncLogEntry{
		Ts: time.Now(), ConnectionID: "gh", Operation: "fast_poll", Outcome: "network",
	})
	s := newTestServer(t, db)
	w := do(t, s, "GET", "/connections")
	var resp connectionsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	h := resp.Connections[0].Health
	if h.Status != healthDegraded {
		t.Errorf("status = %q, want degraded", h.Status)
	}
	if h.FailureCount != 1 {
		t.Errorf("failure_count = %d, want 1", h.FailureCount)
	}
}

func TestConnectionsHealthFailing(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	_ = db.WriteSyncLog(ctx, storage.SyncLogEntry{
		Ts: time.Now(), ConnectionID: "gh", Operation: "fast_poll", Outcome: "auth",
	})
	_ = db.WriteSyncLog(ctx, storage.SyncLogEntry{
		Ts: time.Now(), ConnectionID: "gh", Operation: "fast_poll", Outcome: "network",
	})
	s := newTestServer(t, db)
	w := do(t, s, "GET", "/connections")
	var resp connectionsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	h := resp.Connections[0].Health
	if h.Status != healthFailing {
		t.Errorf("status = %q, want failing", h.Status)
	}
	if h.LastSyncedAt != nil {
		t.Errorf("last_synced_at = %v, want null (no successful sync)", h.LastSyncedAt)
	}
}

// --- GET /sync-log ---

func TestSyncLogEmpty(t *testing.T) {
	s := newTestServer(t, openTestDB(t))
	w := do(t, s, "GET", "/sync-log")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var resp syncLogResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Entries == nil || len(resp.Entries) != 0 {
		t.Errorf("want empty entries array, got %v", resp.Entries)
	}
}

func TestSyncLogReturnsEntries(t *testing.T) {
	db := openTestDB(t)
	_ = db.WriteSyncLog(context.Background(), storage.SyncLogEntry{
		Ts: time.Now(), ConnectionID: "gh", Operation: "fast_poll", Outcome: "ok", ItemsChanged: 3,
	})
	s := newTestServer(t, db)
	w := do(t, s, "GET", "/sync-log")
	var resp syncLogResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Entries) != 1 {
		t.Fatalf("want 1 entry, got %d", len(resp.Entries))
	}
	e := resp.Entries[0]
	if e.ConnectionLabel != "Personal" {
		t.Errorf("connection_label = %q, want Personal", e.ConnectionLabel)
	}
	if e.Outcome != "ok" || e.ItemsChanged != 3 {
		t.Errorf("entry fields wrong: outcome=%q items_changed=%d", e.Outcome, e.ItemsChanged)
	}
}

func TestSyncLogConnectionFilter(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	_ = db.WriteSyncLog(ctx, storage.SyncLogEntry{
		Ts: time.Now(), ConnectionID: "gh", Operation: "fast_poll", Outcome: "ok",
	})
	_ = db.WriteSyncLog(ctx, storage.SyncLogEntry{
		Ts: time.Now(), ConnectionID: "gl", Operation: "fast_poll", Outcome: "ok",
	})
	s := newTestServer(t, db)

	w := do(t, s, "GET", "/sync-log?connection=gh")
	var resp syncLogResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Entries) != 1 || resp.Entries[0].ConnectionID != "gh" {
		t.Errorf("filter by connection: got %d entries", len(resp.Entries))
	}
}

func TestSyncLogOrphanedConnectionLabel(t *testing.T) {
	db := openTestDB(t)
	// Write a row for a connection not in the meta slice.
	_ = db.WriteSyncLog(context.Background(), storage.SyncLogEntry{
		Ts: time.Now(), ConnectionID: "unknown-conn", Operation: "fast_poll", Outcome: "ok",
	})
	s := newTestServer(t, db)
	w := do(t, s, "GET", "/sync-log")
	var resp syncLogResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// Label falls back to the connection_id itself.
	if resp.Entries[0].ConnectionLabel != "unknown-conn" {
		t.Errorf("orphan label = %q, want connection_id", resp.Entries[0].ConnectionLabel)
	}
}

// --- PUT/DELETE /items/{id}/flag ---

func TestFlagSetAndClear(t *testing.T) {
	db := openTestDB(t)
	writeTestEvent(t, db, "acme/api#1", openFacts([]string{"reviewer"}, "requested", ""))
	s := newTestServer(t, db)

	// Resolve the item id by fetching /attention.
	w := do(t, s, "GET", "/attention")
	var attResp attentionResponse
	if err := json.Unmarshal(w.Body.Bytes(), &attResp); err != nil {
		t.Fatalf("decode attention: %v", err)
	}
	if len(attResp.Items) != 1 {
		t.Fatalf("want 1 item, got %d", len(attResp.Items))
	}
	id := attResp.Items[0].ID

	// PUT /items/{id}/flag.
	w = do(t, s, "PUT", "/items/"+id+"/flag")
	if w.Code != http.StatusNoContent {
		t.Fatalf("PUT flag: status = %d, want 204", w.Code)
	}

	// Item should now appear flagged.
	w = do(t, s, "GET", "/attention")
	if err := json.Unmarshal(w.Body.Bytes(), &attResp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !attResp.Items[0].Flagged {
		t.Error("item should be flagged after PUT")
	}

	// DELETE /items/{id}/flag.
	w = do(t, s, "DELETE", "/items/"+id+"/flag")
	if w.Code != http.StatusNoContent {
		t.Fatalf("DELETE flag: status = %d, want 204", w.Code)
	}

	// Item should no longer be flagged.
	w = do(t, s, "GET", "/attention")
	if err := json.Unmarshal(w.Body.Bytes(), &attResp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if attResp.Items[0].Flagged {
		t.Error("item should not be flagged after DELETE")
	}
}

func TestFlagClearIdempotent(t *testing.T) {
	// DELETE on an item that was never flagged should still return 204.
	s := newTestServer(t, openTestDB(t))
	w := do(t, s, "DELETE", "/items/doesnotexist/flag")
	if w.Code != http.StatusNoContent {
		t.Fatalf("DELETE flag: status = %d, want 204", w.Code)
	}
}

// --- New fields: markers, age, since, flagged_at ---

func TestAttentionNewFields(t *testing.T) {
	db := openTestDB(t)
	f := openFacts([]string{"author"}, "", "ready")
	f.MergeConflict = true
	f.GateDetail = "dirty"
	writeTestEvent(t, db, "acme/api#1", f)
	s := newTestServer(t, db)

	w := do(t, s, "GET", "/attention")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var resp attentionResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Items) != 1 {
		t.Fatalf("want 1 item, got %d", len(resp.Items))
	}
	item := resp.Items[0]
	if !item.MergeConflict {
		t.Error("merge_conflict should be true")
	}
	if item.GateDetail != "dirty" {
		t.Errorf("gate_detail = %q, want dirty", item.GateDetail)
	}
	if item.FailingChecks {
		t.Error("failing_checks should be false for merge_conflict item")
	}
}

func TestAttentionFlaggedAtInResponse(t *testing.T) {
	db := openTestDB(t)
	writeTestEvent(t, db, "acme/api#1", openFacts([]string{"reviewer"}, "requested", ""))
	s := newTestServer(t, db)

	w := do(t, s, "GET", "/attention")
	var attResp attentionResponse
	if err := json.Unmarshal(w.Body.Bytes(), &attResp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	id := attResp.Items[0].ID

	w = do(t, s, "PUT", "/items/"+id+"/flag")
	if w.Code != http.StatusNoContent {
		t.Fatalf("PUT flag: status = %d, want 204", w.Code)
	}

	w = do(t, s, "GET", "/attention")
	if err := json.Unmarshal(w.Body.Bytes(), &attResp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if attResp.Items[0].FlaggedAt == nil {
		t.Error("flagged_at should be non-null after pin")
	}
}

// --- GET /up ---

func TestUpReturns200(t *testing.T) {
	s := newTestServer(t, openTestDB(t))
	w := do(t, s, "GET", "/up")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if body := w.Body.String(); body != "ok" {
		t.Errorf("body = %q, want ok", body)
	}
}

// --- update hint on GET /connections ---

func TestConnectionsUpdateAbsentByDefault(t *testing.T) {
	s := newTestServer(t, openTestDB(t))
	w := do(t, s, "GET", "/connections")
	var resp connectionsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Update.Available {
		t.Errorf("update.available = true, want false before any check")
	}
}

func TestConnectionsReflectsSetUpdate(t *testing.T) {
	s := newTestServer(t, openTestDB(t))
	s.SetUpdate(true, "v9.0.0", "https://example/releases/v9.0.0", true)

	w := do(t, s, "GET", "/connections")
	var resp connectionsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	u := resp.Update
	if !u.Available || u.LatestVersion != "v9.0.0" || u.ReleaseURL != "https://example/releases/v9.0.0" || !u.InContainer {
		t.Errorf("update = %+v, want the values passed to SetUpdate", u)
	}
}

// --- 500 paths (DB failure) ---
//
// Closing the DB before the request is the real failure surface (a query
// against a closed *sql.DB), not a mock — storage.DB has no interface seam
// in this package, and the point is to see genuine storage errors turn into
// the errCodeInternal envelope.

func TestAttentionDBErrorReturns500(t *testing.T) {
	db := openTestDB(t)
	s := newTestServer(t, db)
	if err := db.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	w := do(t, s, "GET", "/attention")
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", w.Code)
	}
	var resp errorResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Error != errCodeInternal {
		t.Errorf("error = %q, want %q", resp.Error, errCodeInternal)
	}
}

func TestConnectionsDBErrorReturns500(t *testing.T) {
	db := openTestDB(t)
	s := newTestServer(t, db)
	if err := db.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	w := do(t, s, "GET", "/connections")
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", w.Code)
	}
	var resp errorResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Error != errCodeInternal {
		t.Errorf("error = %q, want %q", resp.Error, errCodeInternal)
	}
}

func TestSyncLogDBErrorReturns500(t *testing.T) {
	db := openTestDB(t)
	s := newTestServer(t, db)
	if err := db.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	w := do(t, s, "GET", "/sync-log")
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", w.Code)
	}
	var resp errorResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Error != errCodeInternal {
		t.Errorf("error = %q, want %q", resp.Error, errCodeInternal)
	}
}

// --- Content-Type ---

func TestContentTypeJSON(t *testing.T) {
	s := newTestServer(t, openTestDB(t))
	for _, path := range []string{"/attention", "/connections", "/sync-log"} {
		w := do(t, s, "GET", path)
		ct := w.Header().Get("Content-Type")
		if ct != "application/json" {
			t.Errorf("GET %s: Content-Type = %q, want application/json", path, ct)
		}
	}
}
