package storage

import (
	"context"
	"testing"
	"time"
)

func TestLatestOutcomesPerOperation(t *testing.T) {
	db := openTest(t)
	ctx := context.Background()
	base := time.Date(2026, 8, 5, 10, 0, 0, 0, time.UTC)

	entries := []SyncLogEntry{
		// fastpoll: three rows; latest is "degraded"
		{Ts: base, ConnectionID: "c1", Operation: "fastpoll", Outcome: "ok"},
		{Ts: base.Add(time.Minute), ConnectionID: "c1", Operation: "fastpoll", Outcome: "ok"},
		{Ts: base.Add(2 * time.Minute), ConnectionID: "c1", Operation: "fastpoll", Outcome: "degraded"},
		// reconcile: two rows; latest is "ok"
		{Ts: base, ConnectionID: "c1", Operation: "reconcile", Outcome: "auth"},
		{Ts: base.Add(time.Minute), ConnectionID: "c1", Operation: "reconcile", Outcome: "ok"},
		// c2 row: must not leak into c1 results
		{Ts: base, ConnectionID: "c2", Operation: "fastpoll", Outcome: "ok"},
	}
	for _, e := range entries {
		if err := db.WriteSyncLog(ctx, e); err != nil {
			t.Fatalf("WriteSyncLog: %v", err)
		}
	}

	got, err := db.LatestOutcomesPerOperation(ctx, "c1")
	if err != nil {
		t.Fatalf("LatestOutcomesPerOperation: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d entries, want 2 (one per operation)", len(got))
	}

	byOp := make(map[string]string, len(got))
	for _, e := range got {
		byOp[e.Operation] = e.Outcome
	}

	if byOp["fastpoll"] != "degraded" {
		t.Errorf("fastpoll outcome = %q, want degraded (latest row wins)", byOp["fastpoll"])
	}
	if byOp["reconcile"] != "ok" {
		t.Errorf("reconcile outcome = %q, want ok (latest row wins)", byOp["reconcile"])
	}
}

func TestLatestOutcomesPerOperationEmpty(t *testing.T) {
	got, err := openTest(t).LatestOutcomesPerOperation(context.Background(), "nonexistent")
	if err != nil {
		t.Fatalf("LatestOutcomesPerOperation: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %d entries, want 0 for unknown connection", len(got))
	}
}
