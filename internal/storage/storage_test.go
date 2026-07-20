package storage

import (
	"context"
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

func ptrInt(v int) *int              { return &v }
func ptrStr(v string) *string        { return &v }
func ptrTime(v time.Time) *time.Time { return &v }

func TestWALMode(t *testing.T) {
	db := openTest(t)
	var mode string
	if err := db.sql.QueryRow(`PRAGMA journal_mode`).Scan(&mode); err != nil {
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
	if err := fdb.sql.QueryRow(`PRAGMA journal_mode`).Scan(&fmode); err != nil {
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
	if err := db2.sql.QueryRow(`SELECT version FROM schema_version`).Scan(&version); err != nil {
		t.Fatalf("read version: %v", err)
	}
	if version != len(migrations) {
		t.Errorf("version = %d, want %d", version, len(migrations))
	}
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
			HTTPStatus: ptrInt(500), Retries: 2, NextRetry: ptrTime(base.Add(2 * time.Minute)),
			Error: ptrStr("boom"), RateRemaining: ptrInt(42)},
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

	ids, err := db.ListHandleNext(ctx)
	if err != nil {
		t.Fatalf("ListHandleNext: %v", err)
	}
	if len(ids) != 3 || ids[0] != "item-a" || ids[1] != "item-b" || ids[2] != "item-c" {
		t.Fatalf("order = %v, want [a b c]", ids)
	}

	// Clearing removes.
	if err := db.SetHandleNext(ctx, "item-b", false); err != nil {
		t.Fatalf("clear b: %v", err)
	}
	ids, _ = db.ListHandleNext(ctx)
	if len(ids) != 2 || ids[0] != "item-a" || ids[1] != "item-c" {
		t.Fatalf("after clear order = %v, want [a c]", ids)
	}

	// Clearing a non-flagged item is a no-op, not an error.
	if err := db.SetHandleNext(ctx, "never-flagged", false); err != nil {
		t.Fatalf("clear absent: %v", err)
	}
}
