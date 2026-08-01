package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"
	"time"

	"github.com/vilaca/devpit/sdk"
)

func openTest(t *testing.T) *DB {
	t.Helper()
	db, err := Open(":memory:")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestWALMode(t *testing.T) {
	db := openTest(t)
	var mode string
	if err := db.write.QueryRowContext(context.Background(), `PRAGMA journal_mode`).Scan(&mode); err != nil {
		t.Fatalf("query journal_mode: %v", err)
	}
	// :memory: databases report "memory"; a WAL request on file DBs reports "wal".
	// The important guarantee is that the PRAGMA ran without error and the DB is
	// usable. Verify a file-backed DB actually lands in WAL.
	fdb, err := Open(t.TempDir() + "/devpit.db")
	if err != nil {
		t.Fatalf("Open file db: %v", err)
	}
	defer func() { _ = fdb.Close() }()
	var fmode string
	if err := fdb.write.QueryRowContext(context.Background(), `PRAGMA journal_mode`).Scan(&fmode); err != nil {
		t.Fatalf("query file journal_mode: %v", err)
	}
	if fmode != "wal" {
		t.Errorf("file journal_mode = %q, want wal", fmode)
	}
	_ = mode
}

func TestMigrationIdempotent(t *testing.T) {
	path := t.TempDir() + "/devpit.db"
	db1, err := Open(path)
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	_ = db1.Close()

	db2, err := Open(path)
	if err != nil {
		t.Fatalf("second Open: %v", err)
	}
	defer func() { _ = db2.Close() }()

	var version int
	if err := db2.write.QueryRowContext(t.Context(), `SELECT version FROM schema_version`).Scan(&version); err != nil {
		t.Fatalf("read version: %v", err)
	}
	if version != len(migrations) {
		t.Errorf("version = %d, want %d", version, len(migrations))
	}
}

func TestOpenRejectsSecondInstance(t *testing.T) {
	path := t.TempDir() + "/devpit.db"
	db1, err := Open(path)
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	defer func() { _ = db1.Close() }()

	// A second Open on the same path, while the first is still open, must fail:
	// two devpit instances on one database is not a supported mode.
	if db2, err := Open(path); err == nil {
		_ = db2.Close()
		t.Fatal("second Open succeeded; want single-instance guard to reject it")
	}

	// After the first closes, the lock is released and reopening works.
	_ = db1.Close()
	db3, err := Open(path)
	if err != nil {
		t.Fatalf("reopen after close: %v", err)
	}
	_ = db3.Close()
}

func TestWriteEventsDedup(t *testing.T) {
	db := openTest(t)
	ctx := context.Background()

	occurred := time.Date(2026, 7, 8, 9, 14, 2, 0, time.UTC)
	ev := sdk.Event{
		ObjectType: "merge_request",
		NativeID:   "acme/api#412",
		EventType:  "item.observed",
		OccurredAt: &occurred,
		Actor:      "jdoe",
		DedupeKey:  "hash-abc",
		Payload:    sdk.ItemObservedPayload{Title: "Fix flaky test", State: "open"},
	}

	n, err := db.WriteEvents(ctx, "conn1", []sdk.Event{ev})
	if err != nil {
		t.Fatalf("first WriteEvents: %v", err)
	}
	if n != 1 {
		t.Errorf("first insert count = %d, want 1", n)
	}

	// Same dedupe_key again -> ignored.
	n, err = db.WriteEvents(ctx, "conn1", []sdk.Event{ev})
	if err != nil {
		t.Fatalf("second WriteEvents: %v", err)
	}
	if n != 0 {
		t.Errorf("dup insert count = %d, want 0", n)
	}

	// Batch with one new + one dup -> 1.
	ev2 := ev
	ev2.DedupeKey = "hash-def"
	n, err = db.WriteEvents(ctx, "conn1", []sdk.Event{ev, ev2})
	if err != nil {
		t.Fatalf("batch WriteEvents: %v", err)
	}
	if n != 1 {
		t.Errorf("batch insert count = %d, want 1", n)
	}

	// Same dedupe_key on a different connection is a distinct row.
	n, err = db.WriteEvents(ctx, "conn2", []sdk.Event{ev})
	if err != nil {
		t.Fatalf("other-conn WriteEvents: %v", err)
	}
	if n != 1 {
		t.Errorf("other-conn insert count = %d, want 1", n)
	}
}

func TestWriteEventsUniqueAcrossEventType(t *testing.T) {
	db := openTest(t)
	ctx := context.Background()

	// Same connection + dedupe_key, but different object_type/native_id/event_type
	// must be distinct rows under the 5-column UNIQUE (§11 step 5). The old
	// (connection_id, dedupe_key) constraint collided item.removed's constant key.
	base := sdk.Event{ObjectType: "merge_request", NativeID: "acme/api#1", DedupeKey: "removed"}
	rm := base
	rm.EventType = "item.removed"
	obs := base
	obs.EventType = "item.observed"
	obs.Payload = sdk.ItemObservedPayload{State: "open"}
	other := base
	other.EventType = "item.removed"
	other.NativeID = "acme/api#2"

	n, err := db.WriteEvents(ctx, "c", []sdk.Event{rm, obs, other})
	if err != nil {
		t.Fatalf("WriteEvents: %v", err)
	}
	if n != 3 {
		t.Errorf("insert count = %d, want 3 (5-column UNIQUE must not collide)", n)
	}

	// The exact same row is still deduped.
	n, err = db.WriteEvents(ctx, "c", []sdk.Event{rm})
	if err != nil {
		t.Fatalf("re-WriteEvents: %v", err)
	}
	if n != 0 {
		t.Errorf("dup insert count = %d, want 0", n)
	}
}

func TestReadPoolIsQueryOnly(t *testing.T) {
	db := openTest(t)
	// The read pool is opened query_only; an attempted write must fail there,
	// guarding against reads and writes being routed to the wrong pool (§14, Q9).
	_, err := db.read.ExecContext(context.Background(),
		`INSERT INTO handle_next (item_id, flagged_at) VALUES ('x', 'y')`)
	if err == nil {
		t.Fatal("write on read pool succeeded, want query_only failure")
	}
}

func TestWriteEventsNilPayloadAndOccurredAt(t *testing.T) {
	db := openTest(t)
	ctx := context.Background()

	ev := sdk.Event{
		ObjectType: "merge_request",
		NativeID:   "acme/api#1",
		EventType:  "item.removed",
		DedupeKey:  "removed",
		// nil payload, nil OccurredAt
	}
	if _, err := db.WriteEvents(ctx, "conn1", []sdk.Event{ev}); err != nil {
		t.Fatalf("WriteEvents: %v", err)
	}

	got, err := db.ReadEvents(ctx, "conn1", time.Time{})
	if err != nil {
		t.Fatalf("ReadEvents: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d events, want 1", len(got))
	}
	if got[0].OccurredAt != nil {
		t.Errorf("OccurredAt = %v, want nil", got[0].OccurredAt)
	}
	if string(got[0].Payload) != "{}" {
		t.Errorf("Payload = %q, want {}", string(got[0].Payload))
	}
}

func TestReadEventsSince(t *testing.T) {
	db := openTest(t)
	ctx := context.Background()

	if _, err := db.WriteEvents(ctx, "c", []sdk.Event{{
		ObjectType: "issue", NativeID: "n1", EventType: "item.observed",
		DedupeKey: "k1", Payload: map[string]string{"a": "b"},
	}}); err != nil {
		t.Fatalf("WriteEvents: %v", err)
	}

	// since in the future -> nothing.
	future := time.Now().UTC().Add(time.Hour)
	got, err := db.ReadEvents(ctx, "c", future)
	if err != nil {
		t.Fatalf("ReadEvents future: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("future since returned %d events, want 0", len(got))
	}

	// zero time -> all.
	got, err = db.ReadEvents(ctx, "c", time.Time{})
	if err != nil {
		t.Fatalf("ReadEvents all: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("all returned %d events, want 1", len(got))
	}
}

func TestCursorsRoundTrip(t *testing.T) {
	db := openTest(t)
	ctx := context.Background()

	// Empty when none exist, non-nil.
	got, err := db.LoadCursors(ctx, "conn1")
	if err != nil {
		t.Fatalf("LoadCursors empty: %v", err)
	}
	if got == nil {
		t.Fatal("LoadCursors returned nil map")
	}
	if len(got) != 0 {
		t.Errorf("LoadCursors empty len = %d, want 0", len(got))
	}

	state := sdk.PollState{"fast.last_modified": "abc", "rec.mr_updated_after": "2026-07-08"}
	if err := db.SaveCursors(ctx, "conn1", state); err != nil {
		t.Fatalf("SaveCursors: %v", err)
	}

	got, err = db.LoadCursors(ctx, "conn1")
	if err != nil {
		t.Fatalf("LoadCursors: %v", err)
	}
	if len(got) != 2 || got["fast.last_modified"] != "abc" || got["rec.mr_updated_after"] != "2026-07-08" {
		t.Errorf("round-trip mismatch: %v", got)
	}

	// Upsert overwrites.
	if err := db.SaveCursors(ctx, "conn1", sdk.PollState{"fast.last_modified": "xyz"}); err != nil {
		t.Fatalf("SaveCursors upsert: %v", err)
	}
	got, _ = db.LoadCursors(ctx, "conn1")
	if got["fast.last_modified"] != "xyz" {
		t.Errorf("upsert failed: %v", got)
	}
	if got["rec.mr_updated_after"] != "2026-07-08" {
		t.Errorf("upsert clobbered unrelated key: %v", got)
	}

	// Isolation by connection.
	other, _ := db.LoadCursors(ctx, "conn2")
	if len(other) != 0 {
		t.Errorf("conn2 leaked cursors: %v", other)
	}
}

func TestSyncLogOrdering(t *testing.T) {
	db := openTest(t)
	ctx := context.Background()

	base := time.Date(2026, 7, 8, 10, 0, 0, 0, time.UTC)
	entries := []SyncLogEntry{
		{Ts: base, ConnectionID: "c1", Operation: "fast_poll", Outcome: "ok", ItemsChanged: 3},
		{Ts: base.Add(time.Minute), ConnectionID: "c1", Operation: "reconcile", Outcome: "error",
			HTTPStatus: new(500), Retries: 2, NextRetry: new(base.Add(2 * time.Minute)),
			Error: new("boom"), RateRemaining: new(42)},
		{Ts: base.Add(2 * time.Minute), ConnectionID: "c2", Operation: "fast_poll", Outcome: "ok"},
	}
	for _, e := range entries {
		if err := db.WriteSyncLog(ctx, e); err != nil {
			t.Fatalf("WriteSyncLog: %v", err)
		}
	}

	// All connections, newest first.
	all, err := db.ReadSyncLog(ctx, "", 10)
	if err != nil {
		t.Fatalf("ReadSyncLog all: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("got %d rows, want 3", len(all))
	}
	if all[0].ConnectionID != "c2" || all[2].Operation != "fast_poll" {
		t.Errorf("ordering wrong: %+v", all)
	}

	// Filter by connection.
	c1, err := db.ReadSyncLog(ctx, "c1", 10)
	if err != nil {
		t.Fatalf("ReadSyncLog c1: %v", err)
	}
	if len(c1) != 2 {
		t.Fatalf("c1 got %d rows, want 2", len(c1))
	}
	// Newest c1 row is the reconcile error; check nullable fields round-trip.
	top := c1[0]
	if top.Operation != "reconcile" || top.Outcome != "error" {
		t.Errorf("c1 top row = %+v", top)
	}
	if top.HTTPStatus == nil || *top.HTTPStatus != 500 {
		t.Errorf("HTTPStatus = %v, want 500", top.HTTPStatus)
	}
	if top.RateRemaining == nil || *top.RateRemaining != 42 {
		t.Errorf("RateRemaining = %v, want 42", top.RateRemaining)
	}
	if top.NextRetry == nil || !top.NextRetry.Equal(base.Add(2*time.Minute)) {
		t.Errorf("NextRetry = %v", top.NextRetry)
	}
	if top.Error == nil || *top.Error != "boom" {
		t.Errorf("Error = %v", top.Error)
	}
	if top.Retries != 2 {
		t.Errorf("Retries = %d, want 2", top.Retries)
	}

	// Bottom c1 row is the ok fast_poll; nullables must be nil.
	bottom := c1[1]
	if bottom.HTTPStatus != nil || bottom.RateRemaining != nil || bottom.NextRetry != nil || bottom.Error != nil {
		t.Errorf("ok row should have nil nullables: %+v", bottom)
	}
	if bottom.ItemsChanged != 3 {
		t.Errorf("ItemsChanged = %d, want 3", bottom.ItemsChanged)
	}

	// Limit is honored.
	limited, err := db.ReadSyncLog(ctx, "", 1)
	if err != nil {
		t.Fatalf("ReadSyncLog limit: %v", err)
	}
	if len(limited) != 1 {
		t.Errorf("limit 1 returned %d rows", len(limited))
	}
}

func TestReadSyncLogSince(t *testing.T) {
	db := openTest(t)
	ctx := context.Background()

	base := time.Date(2026, 7, 8, 10, 0, 0, 0, time.UTC)
	// c1 rows straddle the boundary; the c2 row must never leak into a c1 query.
	entries := []SyncLogEntry{
		{Ts: base, ConnectionID: "c1", Operation: "fast_poll", Outcome: "ok"},
		{Ts: base.Add(time.Minute), ConnectionID: "c1", Operation: "reconcile", Outcome: "ok"},
		{Ts: base.Add(2 * time.Minute), ConnectionID: "c1", Operation: "fast_poll", Outcome: "error", HTTPStatus: new(500)},
		{Ts: base.Add(time.Minute), ConnectionID: "c2", Operation: "fast_poll", Outcome: "ok"},
	}
	for _, e := range entries {
		if err := db.WriteSyncLog(ctx, e); err != nil {
			t.Fatalf("WriteSyncLog: %v", err)
		}
	}

	// since == the middle row's ts: that row is included (>=), the earlier one is not.
	got, err := db.ReadSyncLogSince(ctx, "c1", base.Add(time.Minute))
	if err != nil {
		t.Fatalf("ReadSyncLogSince: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d rows, want 2 (boundary inclusive, base row excluded)", len(got))
	}
	// Newest first.
	if !got[0].Ts.Equal(base.Add(2 * time.Minute)) {
		t.Errorf("got[0].Ts = %v, want %v", got[0].Ts, base.Add(2*time.Minute))
	}
	if !got[1].Ts.Equal(base.Add(time.Minute)) {
		t.Errorf("got[1].Ts = %v, want %v", got[1].Ts, base.Add(time.Minute))
	}
	for _, e := range got {
		if e.ConnectionID != "c1" {
			t.Errorf("leaked connection %q into c1 query", e.ConnectionID)
		}
	}

	// A since past every row returns nothing.
	empty, err := db.ReadSyncLogSince(ctx, "c1", base.Add(time.Hour))
	if err != nil {
		t.Fatalf("ReadSyncLogSince future: %v", err)
	}
	if len(empty) != 0 {
		t.Errorf("future since returned %d rows, want 0", len(empty))
	}
}

func TestLastSyncedAt(t *testing.T) {
	db := openTest(t)
	ctx := context.Background()

	// No rows yet -> zero time, no error.
	ts, err := db.LastSyncedAt(ctx, "c1")
	if err != nil {
		t.Fatalf("LastSyncedAt empty: %v", err)
	}
	if !ts.IsZero() {
		t.Errorf("ts = %v, want zero for no rows", ts)
	}

	base := time.Date(2026, 7, 8, 10, 0, 0, 0, time.UTC)
	// The latest successful sync is the middle row; a later error must not count.
	entries := []SyncLogEntry{
		{Ts: base, ConnectionID: "c1", Operation: "fast_poll", Outcome: "error", HTTPStatus: new(500)},
		{Ts: base.Add(time.Minute), ConnectionID: "c1", Operation: "reconcile", Outcome: "ok"},
		{Ts: base.Add(2 * time.Minute), ConnectionID: "c1", Operation: "fast_poll", Outcome: "error", HTTPStatus: new(503)},
	}
	for _, e := range entries {
		if err := db.WriteSyncLog(ctx, e); err != nil {
			t.Fatalf("WriteSyncLog: %v", err)
		}
	}

	ts, err = db.LastSyncedAt(ctx, "c1")
	if err != nil {
		t.Fatalf("LastSyncedAt: %v", err)
	}
	if !ts.Equal(base.Add(time.Minute)) {
		t.Errorf("ts = %v, want %v (latest ok, ignoring later error)", ts, base.Add(time.Minute))
	}

	// A connection with only failures has no successful sync.
	if err := db.WriteSyncLog(ctx, SyncLogEntry{
		Ts: base, ConnectionID: "c2", Operation: "fast_poll", Outcome: "error", HTTPStatus: new(500),
	}); err != nil {
		t.Fatalf("WriteSyncLog c2: %v", err)
	}
	ts, err = db.LastSyncedAt(ctx, "c2")
	if err != nil {
		t.Fatalf("LastSyncedAt c2: %v", err)
	}
	if !ts.IsZero() {
		t.Errorf("c2 ts = %v, want zero (no successful sync)", ts)
	}
}

// TestLastSyncedAtCountsDegraded guards B2: a degraded cycle is a success (it
// persisted events + cursors, ADR-0024), so a connection with only degraded
// rows must report its latest degraded ts, not never-synced.
func TestLastSyncedAtCountsDegraded(t *testing.T) {
	db := openTest(t)
	ctx := context.Background()

	base := time.Date(2026, 7, 8, 10, 0, 0, 0, time.UTC)
	for _, e := range []SyncLogEntry{
		{Ts: base, ConnectionID: "c1", Operation: "reconcile", Outcome: "degraded"},
		{Ts: base.Add(time.Minute), ConnectionID: "c1", Operation: "reconcile", Outcome: "degraded"},
	} {
		if err := db.WriteSyncLog(ctx, e); err != nil {
			t.Fatalf("WriteSyncLog: %v", err)
		}
	}

	ts, err := db.LastSyncedAt(ctx, "c1")
	if err != nil {
		t.Fatalf("LastSyncedAt: %v", err)
	}
	if !ts.Equal(base.Add(time.Minute)) {
		t.Errorf("ts = %v, want %v (latest degraded counts as synced)", ts, base.Add(time.Minute))
	}
}

func TestMigrationUpgrade(t *testing.T) {
	// Open creates a fresh DB at the latest schema version; verify the
	// jira_tickets table (migration 2) exists and accepts a round-trip.
	db := openTest(t)
	ctx := context.Background()

	var version int
	if err := db.write.QueryRowContext(ctx, `SELECT version FROM schema_version`).Scan(&version); err != nil {
		t.Fatalf("read schema_version: %v", err)
	}
	if version != len(migrations) {
		t.Fatalf("schema_version = %d, want %d", version, len(migrations))
	}

	// Upsert then read back.
	now := time.Date(2026, 7, 13, 10, 0, 0, 0, time.UTC)
	tk := JiraTicket{Key: "RPC-1", Status: "In Review", Summary: "Fix it", Assignee: "bob",
		URL: "https://example.atlassian.net/browse/RPC-1", FetchedAt: now}
	if err := db.UpsertJiraTicket(ctx, tk); err != nil {
		t.Fatalf("UpsertJiraTicket: %v", err)
	}
	got, err := db.GetJiraTickets(ctx, []string{"RPC-1"})
	if err != nil {
		t.Fatalf("GetJiraTickets: %v", err)
	}
	row, ok := got["RPC-1"]
	if !ok {
		t.Fatal("RPC-1 missing from GetJiraTickets result")
	}
	if row.Status != "In Review" || row.Summary != "Fix it" || row.Assignee != "bob" {
		t.Errorf("row = %+v", row)
	}
	if !row.FetchedAt.Equal(now) {
		t.Errorf("FetchedAt = %v, want %v", row.FetchedAt, now)
	}
	if row.FetchError != nil {
		t.Errorf("FetchError = %v, want nil", row.FetchError)
	}
}

// TestMigrationIncrementalUpgrade seeds a raw DB at schema_version 1 (only the
// first migration applied) and asserts migrate steps through the remaining
// migrations to the latest version — the incremental upgrade loop that a fresh
// Open (already at latest) never exercises (B7).
func TestMigrationIncrementalUpgrade(t *testing.T) {
	ctx := context.Background()
	writeDSN, _ := dsns(":memory:")
	raw, err := sql.Open("sqlite", writeDSN)
	if err != nil {
		t.Fatalf("open raw: %v", err)
	}
	raw.SetMaxOpenConns(1) // keep the shared-cache in-memory DB alive
	t.Cleanup(func() { _ = raw.Close() })

	// Apply only migration 1 by hand and stamp the DB at version 1.
	if _, err := raw.ExecContext(ctx, `CREATE TABLE schema_version (version INTEGER NOT NULL)`); err != nil {
		t.Fatalf("create schema_version: %v", err)
	}
	if _, err := raw.ExecContext(ctx, migrations[0]); err != nil {
		t.Fatalf("apply migration 1: %v", err)
	}
	if _, err := raw.ExecContext(ctx, `INSERT INTO schema_version (version) VALUES (1)`); err != nil {
		t.Fatalf("seed version: %v", err)
	}

	// migrate must run migrations 2..N incrementally. (Do not call db.Close:
	// this DB has no read pool or file lock — closing raw directly is enough.)
	db := &DB{write: raw}
	if err := db.migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	var version int
	if err := raw.QueryRowContext(ctx, `SELECT version FROM schema_version`).Scan(&version); err != nil {
		t.Fatalf("read version: %v", err)
	}
	if version != len(migrations) {
		t.Errorf("version = %d, want %d", version, len(migrations))
	}

	// The tables from migrations 2 (jira_tickets) and 3 (repo_approvers) exist.
	now := time.Date(2026, 7, 13, 10, 0, 0, 0, time.UTC)
	if err := db.UpsertJiraTicket(ctx, JiraTicket{Key: "RPC-1", FetchedAt: now}); err != nil {
		t.Errorf("jira_tickets (migration 2) missing: %v", err)
	}
	if err := db.UpsertRepoApprover(ctx, RepoApprover{ConnectionID: "c1", Repo: "r", FetchedAt: now}); err != nil {
		t.Errorf("repo_approvers (migration 3) missing: %v", err)
	}
}

func TestJiraTicketUpsertStaleBeatsBlank(t *testing.T) {
	db := openTest(t)
	ctx := context.Background()
	now := time.Date(2026, 7, 13, 10, 0, 0, 0, time.UTC)

	// Insert a good row.
	good := JiraTicket{Key: "RPC-2", Status: "Done", Summary: "All fixed",
		URL: "https://x/browse/RPC-2", FetchedAt: now}
	if err := db.UpsertJiraTicket(ctx, good); err != nil {
		t.Fatalf("initial upsert: %v", err)
	}

	// Upsert with an error — stale status/summary must be preserved.
	errStr := "status 500"
	bad := JiraTicket{Key: "RPC-2", FetchedAt: now.Add(time.Minute), FetchError: &errStr}
	if err := db.UpsertJiraTicket(ctx, bad); err != nil {
		t.Fatalf("error upsert: %v", err)
	}
	got, err := db.GetJiraTickets(ctx, []string{"RPC-2"})
	if err != nil {
		t.Fatalf("GetJiraTickets: %v", err)
	}
	row := got["RPC-2"]
	if row.Status != "Done" {
		t.Errorf("status = %q, want %q (stale beats blank)", row.Status, "Done")
	}
	if row.FetchError == nil || *row.FetchError != errStr {
		t.Errorf("FetchError = %v, want %q", row.FetchError, errStr)
	}
}

func TestJiraTicketPrune(t *testing.T) {
	db := openTest(t)
	ctx := context.Background()
	now := time.Date(2026, 7, 13, 10, 0, 0, 0, time.UTC)

	for _, key := range []string{"A-1", "B-2", "C-3"} {
		if err := db.UpsertJiraTicket(ctx, JiraTicket{Key: key, FetchedAt: now}); err != nil {
			t.Fatalf("upsert %s: %v", key, err)
		}
	}

	// Prune, keeping only A-1 and B-2.
	if err := db.PruneJiraTickets(ctx, []string{"A-1", "B-2"}); err != nil {
		t.Fatalf("PruneJiraTickets: %v", err)
	}
	got, err := db.GetJiraTickets(ctx, []string{"A-1", "B-2", "C-3"})
	if err != nil {
		t.Fatalf("GetJiraTickets after prune: %v", err)
	}
	if _, ok := got["C-3"]; ok {
		t.Error("C-3 should have been pruned")
	}
	if _, ok := got["A-1"]; !ok {
		t.Error("A-1 should survive prune")
	}
}

func TestAllOpenTicketKeys(t *testing.T) {
	db := openTest(t)
	ctx := context.Background()

	// Write two item.observed events: one open with keys, one closed.
	events := []sdk.Event{
		{
			ObjectType: "merge_request", NativeID: "conn1/api#1",
			EventType: "item.observed", DedupeKey: "k1",
			Payload: sdk.ItemObservedPayload{
				State: "open", Title: "Fix", TicketKeys: []string{"RPC-1", "RPC-2"},
			},
		},
		{
			ObjectType: "merge_request", NativeID: "conn1/api#2",
			EventType: "item.observed", DedupeKey: "k2",
			Payload: sdk.ItemObservedPayload{State: "merged", TicketKeys: []string{"RPC-9"}},
		},
		{
			ObjectType: "merge_request", NativeID: "conn1/api#3",
			EventType: "item.observed", DedupeKey: "k3",
			Payload: sdk.ItemObservedPayload{State: "open"},
		},
		// #4 was observed open with a key, then reaped (item.removed): its latest
		// fact is the removal, so its key must not leak (ADR-0024, B1 regression).
		{
			ObjectType: "merge_request", NativeID: "conn1/api#4",
			EventType: "item.observed", DedupeKey: "k4",
			Payload: sdk.ItemObservedPayload{State: "open", TicketKeys: []string{"RPC-7"}},
		},
		{
			ObjectType: "merge_request", NativeID: "conn1/api#4",
			EventType: "item.removed", DedupeKey: "r4",
		},
	}
	if _, err := db.WriteEvents(ctx, "conn1", events); err != nil {
		t.Fatalf("WriteEvents: %v", err)
	}

	keys, err := db.AllOpenTicketKeys(ctx)
	if err != nil {
		t.Fatalf("AllOpenTicketKeys: %v", err)
	}
	got := map[string]bool{}
	for _, k := range keys {
		got[k] = true
	}
	if !got["RPC-1"] || !got["RPC-2"] {
		t.Errorf("keys = %v, want RPC-1 and RPC-2 (still-open item)", keys)
	}
	if got["RPC-9"] {
		t.Error("RPC-9 from merged item should not appear")
	}
	if got["RPC-7"] {
		t.Error("RPC-7 from a reaped (item.removed) item must not appear")
	}
}

func TestLatestItemFacts(t *testing.T) {
	db := openTest(t)
	ctx := context.Background()

	// #1 open (latest fact is observed-open); #2 merged; #3 removed after an
	// observed; #4 open with a *newer signal* (signals must be ignored — the
	// latest fact is still the observed-open); #5 signals only (no fact event).
	events := []sdk.Event{
		{ObjectType: "merge_request", NativeID: "g/p#1", EventType: "item.observed",
			DedupeKey: "o1", Payload: sdk.ItemObservedPayload{State: "open", MyRoles: []string{"author"}}},
		{ObjectType: "merge_request", NativeID: "g/p#2", EventType: "item.observed",
			DedupeKey: "o2a", Payload: sdk.ItemObservedPayload{State: "open", MyRoles: []string{"reviewer"}}},
		{ObjectType: "merge_request", NativeID: "g/p#2", EventType: "item.observed",
			DedupeKey: "o2b", Payload: sdk.ItemObservedPayload{State: "merged"}},
		{ObjectType: "merge_request", NativeID: "g/p#3", EventType: "item.observed",
			DedupeKey: "o3", Payload: sdk.ItemObservedPayload{State: "open", MyRoles: []string{"author"}}},
		{ObjectType: "merge_request", NativeID: "g/p#3", EventType: "item.removed", DedupeKey: "r3"},
		{ObjectType: "merge_request", NativeID: "g/p#4", EventType: "item.observed",
			DedupeKey: "o4", Payload: sdk.ItemObservedPayload{State: "open"}}, // role-less
		{ObjectType: "merge_request", NativeID: "g/p#4", EventType: "signal.mentioned",
			DedupeKey: "s4", Payload: sdk.SignalMentionedPayload{Direct: true}},
		{ObjectType: "merge_request", NativeID: "g/p#5", EventType: "signal.mentioned",
			DedupeKey: "s5", Payload: sdk.SignalMentionedPayload{Direct: true}},
	}
	if _, err := db.WriteEvents(ctx, "c1", events); err != nil {
		t.Fatalf("WriteEvents: %v", err)
	}
	// A different connection must never leak into the result.
	if _, err := db.WriteEvents(ctx, "c2", []sdk.Event{
		{ObjectType: "merge_request", NativeID: "g/p#1", EventType: "item.observed",
			DedupeKey: "o1", Payload: sdk.ItemObservedPayload{State: "open", MyRoles: []string{"author"}}},
	}); err != nil {
		t.Fatalf("WriteEvents c2: %v", err)
	}

	facts, err := db.LatestItemFacts(ctx, "c1")
	if err != nil {
		t.Fatalf("LatestItemFacts: %v", err)
	}

	byID := map[string]ItemFact{}
	for _, f := range facts {
		byID[f.NativeID] = f
	}
	// #5 has no fact event, so it is absent entirely.
	if len(facts) != 4 {
		t.Fatalf("got %d facts, want 4 (one per item with a fact event): %+v", len(facts), facts)
	}
	if f := byID["g/p#1"]; f.EventType != "item.observed" {
		t.Errorf("#1 latest fact = %q, want item.observed", f.EventType)
	}
	if f := byID["g/p#2"]; f.EventType != "item.observed" {
		t.Errorf("#2 latest fact type = %q, want item.observed (merged snapshot)", f.EventType)
	} else {
		var pl sdk.ItemObservedPayload
		_ = json.Unmarshal(f.Payload, &pl)
		if pl.State != "merged" {
			t.Errorf("#2 state = %q, want merged", pl.State)
		}
	}
	if f := byID["g/p#3"]; f.EventType != "item.removed" {
		t.Errorf("#3 latest fact = %q, want item.removed", f.EventType)
	}
	// #4: the newer mention signal must not hide the observed-open fact.
	if f := byID["g/p#4"]; f.EventType != "item.observed" {
		t.Errorf("#4 latest fact = %q, want item.observed (signals ignored)", f.EventType)
	}
	if _, ok := byID["g/p#5"]; ok {
		t.Error("#5 (signals only) must not appear — it has no fact event")
	}
}

func TestHandleNextOrdering(t *testing.T) {
	db := openTest(t)
	ctx := context.Background()

	// Flag three items with increasing flagged_at (enforce via distinct sleeps
	// would be flaky; instead flag in order and rely on time monotonicity plus
	// the item_id tiebreak).
	if err := db.SetHandleNext(ctx, "item-a", true); err != nil {
		t.Fatalf("flag a: %v", err)
	}
	time.Sleep(2 * time.Millisecond)
	if err := db.SetHandleNext(ctx, "item-b", true); err != nil {
		t.Fatalf("flag b: %v", err)
	}
	time.Sleep(2 * time.Millisecond)
	if err := db.SetHandleNext(ctx, "item-c", true); err != nil {
		t.Fatalf("flag c: %v", err)
	}

	pinned, err := db.ListHandleNext(ctx)
	if err != nil {
		t.Fatalf("ListHandleNext: %v", err)
	}
	if len(pinned) != 3 || pinned[0].ID != "item-a" || pinned[1].ID != "item-b" || pinned[2].ID != "item-c" {
		t.Fatalf("order = %v, want [a b c]", pinned)
	}
	// FlaggedAt should be set on each entry.
	for _, p := range pinned {
		if p.FlaggedAt.IsZero() {
			t.Errorf("FlaggedAt zero for %s", p.ID)
		}
	}

	// Clearing removes.
	if err := db.SetHandleNext(ctx, "item-b", false); err != nil {
		t.Fatalf("clear b: %v", err)
	}
	pinned, _ = db.ListHandleNext(ctx)
	if len(pinned) != 2 || pinned[0].ID != "item-a" || pinned[1].ID != "item-c" {
		t.Fatalf("after clear order = %v, want [a c]", pinned)
	}

	// Clearing a non-flagged item is a no-op, not an error.
	if err := db.SetHandleNext(ctx, "never-flagged", false); err != nil {
		t.Fatalf("clear absent: %v", err)
	}
}

func TestRepoApprovers(t *testing.T) {
	db := openTest(t)
	ctx := context.Background()

	now := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	sample := RepoApprover{
		ConnectionID:   "conn1",
		Repo:           "acme/api",
		IsSoleApprover: true,
		FetchedAt:      now,
	}

	// Insert and verify round-trip.
	if err := db.UpsertRepoApprover(ctx, sample); err != nil {
		t.Fatalf("UpsertRepoApprover: %v", err)
	}
	got, found, err := db.GetRepoApprover(ctx, "conn1", "acme/api")
	if err != nil {
		t.Fatalf("GetRepoApprover: %v", err)
	}
	if !found {
		t.Fatal("GetRepoApprover: found = false, want true")
	}
	if got.ConnectionID != sample.ConnectionID || got.Repo != sample.Repo {
		t.Errorf("identity mismatch: %+v", got)
	}
	if !got.IsSoleApprover {
		t.Errorf("IsSoleApprover = false, want true")
	}
	if !got.FetchedAt.Equal(now) {
		t.Errorf("FetchedAt = %v, want %v", got.FetchedAt, now)
	}

	// Upsert with IsSoleApprover=false — should update.
	updated := sample
	updated.IsSoleApprover = false
	updated.FetchedAt = now.Add(time.Minute)
	if err := db.UpsertRepoApprover(ctx, updated); err != nil {
		t.Fatalf("UpsertRepoApprover (update): %v", err)
	}
	got, found, err = db.GetRepoApprover(ctx, "conn1", "acme/api")
	if err != nil {
		t.Fatalf("GetRepoApprover after update: %v", err)
	}
	if !found {
		t.Fatal("GetRepoApprover after update: found = false, want true")
	}
	if got.IsSoleApprover {
		t.Errorf("IsSoleApprover = true after update, want false")
	}
	if !got.FetchedAt.Equal(now.Add(time.Minute)) {
		t.Errorf("FetchedAt = %v, want %v", got.FetchedAt, now.Add(time.Minute))
	}

	// Non-existent key -> found=false, no error.
	_, found, err = db.GetRepoApprover(ctx, "conn1", "no/such-repo")
	if err != nil {
		t.Fatalf("GetRepoApprover missing: %v", err)
	}
	if found {
		t.Error("GetRepoApprover missing: found = true, want false")
	}
}
