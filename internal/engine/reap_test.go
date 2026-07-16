package engine

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/vilaca/devpit/internal/storage"
	"github.com/vilaca/devpit/sdk"
)

// The reap tests use a real storage.DB (not fakeStore): reaping depends on the
// LatestItemFacts query, the INSERT OR IGNORE dedupe on WriteEvents, and salted
// resurrection — behaviours a fake would only re-encode. The fold-level view of
// resurrection lives in internal/attention; here we assert on the event log.

func openReapDB(t *testing.T) *storage.DB {
	t.Helper()
	db, err := storage.Open(":memory:")
	if err != nil {
		t.Fatalf("open storage: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// nid is the single item the reap tests operate on. One item is enough to
// exercise every reap/resurrection branch, and a constant keeps the assertions
// terse.
const nid = "g/p#1"

// observed builds an item.observed event for nid whose dedupe key is stable for
// a given roles fact set, so a re-observation with the same facts collides on
// INSERT OR IGNORE — the case salted resurrection must defeat.
func observed(roles ...string) sdk.Event {
	pl := sdk.ItemObservedPayload{State: itemStateOpen, Title: "t", MyRoles: roles}
	return sdk.Event{
		ObjectType: "merge_request",
		NativeID:   nid,
		EventType:  eventItemObserved,
		DedupeKey:  "obs:" + nid,
		Payload:    pl,
	}
}

func seed(t *testing.T, db *storage.DB, events ...sdk.Event) {
	t.Helper()
	if _, err := db.WriteEvents(context.Background(), "c1", events); err != nil {
		t.Fatalf("seed WriteEvents: %v", err)
	}
}

func storedEvents(t *testing.T, db *storage.DB) []storage.StoredEvent {
	t.Helper()
	evs, err := db.ReadEvents(context.Background(), "c1", time.Time{})
	if err != nil {
		t.Fatalf("ReadEvents: %v", err)
	}
	return evs
}

func removals(evs []storage.StoredEvent) []storage.StoredEvent {
	var out []storage.StoredEvent
	for _, e := range evs {
		if e.EventType == eventItemRemoved && e.NativeID == nid {
			out = append(out, e)
		}
	}
	return out
}

// latestFactOpen reports whether nid's latest fact event (max id among
// observed/removed) is an observed-open — i.e. the fold would show it.
func latestFactOpen(evs []storage.StoredEvent) bool {
	var latest *storage.StoredEvent
	for i := range evs {
		e := &evs[i]
		if e.NativeID != nid {
			continue
		}
		if e.EventType != eventItemObserved && e.EventType != eventItemRemoved {
			continue
		}
		if latest == nil || e.ID > latest.ID {
			latest = e
		}
	}
	if latest == nil || latest.EventType != eventItemObserved {
		return false
	}
	var pl sdk.ItemObservedPayload
	if json.Unmarshal(latest.Payload, &pl) != nil {
		return false
	}
	return pl.State == itemStateOpen
}

func reconcileResult(complete, degraded bool, events ...sdk.Event) sdk.PollResult {
	return sdk.PollResult{Events: events, Complete: complete, Degraded: degraded}
}

// 1. A complete sweep that omits a roled open item reaps it and fires the notify.
func TestReapCompleteSweepRemovesMissingItem(t *testing.T) {
	db := openReapDB(t)
	seed(t, db, observed("author"))

	prov := &fakeProvider{result: reconcileResult(true, false)} // empty sweep
	notify := &recordNotifier{}
	c := newTestConn(db, prov, notify)

	c.cycle(context.Background(), opReconcile, false)

	if got := removals(storedEvents(t, db)); len(got) != 1 {
		t.Fatalf("removals for the item = %d, want 1", len(got))
	}
	if notify.attention != 1 {
		t.Errorf("AttentionChanged fired %d times, want 1", notify.attention)
	}
	if latestFactOpen(storedEvents(t, db)) {
		t.Error("item should be reaped (latest fact removed), but fold would still show it")
	}
}

// 2. An incomplete sweep (Complete=false) never reaps, even if the item is absent.
func TestReapIncompleteSweepDoesNotReap(t *testing.T) {
	db := openReapDB(t)
	seed(t, db, observed("author"))

	prov := &fakeProvider{result: reconcileResult(false, false)}
	c := newTestConn(db, prov, &recordNotifier{})

	c.cycle(context.Background(), opReconcile, false)

	if got := removals(storedEvents(t, db)); len(got) != 0 {
		t.Errorf("removals = %d, want 0 on an incomplete sweep", len(got))
	}
}

// 3. Degraded enrichment does not block reaping — the reap set is the REST
// identity set, independent of GraphQL degradation (ADR-0024).
func TestReapDegradedButCompleteStillReaps(t *testing.T) {
	db := openReapDB(t)
	seed(t, db, observed("author"))

	prov := &fakeProvider{result: reconcileResult(true, true)} // Complete + Degraded
	c := newTestConn(db, prov, &recordNotifier{})

	c.cycle(context.Background(), opReconcile, false)

	if got := removals(storedEvents(t, db)); len(got) != 1 {
		t.Errorf("removals = %d, want 1 (degraded must not suppress reaping)", len(got))
	}
}

// 4. A mention-only (role-less) item is outside reconcile's authority and is
// never reaped.
func TestReapMentionOnlyItemExempt(t *testing.T) {
	db := openReapDB(t)
	seed(t, db, observed()) // no roles

	prov := &fakeProvider{result: reconcileResult(true, false)}
	c := newTestConn(db, prov, &recordNotifier{})

	c.cycle(context.Background(), opReconcile, false)

	if got := removals(storedEvents(t, db)); len(got) != 0 {
		t.Errorf("removals = %d, want 0 for a role-less item", len(got))
	}
}

// 5. An already-removed item still absent from the sweep is not removed again.
func TestReapAlreadyRemovedIsIdempotent(t *testing.T) {
	db := openReapDB(t)
	seed(t, db, observed("author"))
	// First complete sweep reaps it.
	c := newTestConn(db, &fakeProvider{result: reconcileResult(true, false)}, &recordNotifier{})
	c.cycle(context.Background(), opReconcile, false)
	if got := removals(storedEvents(t, db)); len(got) != 1 {
		t.Fatalf("after first sweep removals = %d, want 1", len(got))
	}
	// Second complete sweep, still absent: no second removal.
	c.cycle(context.Background(), opReconcile, false)
	if got := removals(storedEvents(t, db)); len(got) != 1 {
		t.Errorf("after second sweep removals = %d, want 1 (idempotent)", len(got))
	}
}

// 6. Resurrection: a swept item whose latest fact is a removal is re-observed
// with an *identical* fact set. Without salting, INSERT OR IGNORE would drop it
// and the removal would stay latest forever; salting inserts a superseding
// snapshot, so the fold shows the item again (the false-reap self-heal).
func TestReapResurrectionIdenticalPayload(t *testing.T) {
	db := openReapDB(t)
	seed(t, db, observed("author"))
	// Reap it (produces the removal via the engine, keyed to the observed's id).
	c := newTestConn(db, &fakeProvider{result: reconcileResult(true, false)}, &recordNotifier{})
	c.cycle(context.Background(), opReconcile, false)
	if latestFactOpen(storedEvents(t, db)) {
		t.Fatal("precondition: item should be reaped before resurrection")
	}

	// Sweep now re-observes the item with the same facts (same dedupe key).
	c2 := newTestConn(db, &fakeProvider{result: reconcileResult(true, false, observed("author"))}, &recordNotifier{})
	c2.cycle(context.Background(), opReconcile, false)

	evs := storedEvents(t, db)
	if !latestFactOpen(evs) {
		t.Fatal("resurrection failed: identical re-observation was dropped, fold still hides the item")
	}
	var salted int
	for _, e := range evs {
		if e.EventType == eventItemObserved && e.NativeID == nid && strings.Contains(e.DedupeKey, ":resurrect:") {
			salted++
		}
	}
	if salted != 1 {
		t.Errorf("salted resurrection snapshots = %d, want 1", salted)
	}
}

// 7. Reopen → re-merge yields two removals with distinct keys, and the fold ends
// with the item dropped.
func TestReapReopenRemergeDistinctRemovals(t *testing.T) {
	db := openReapDB(t)
	seed(t, db, observed("author"))

	step := func(events ...sdk.Event) {
		c := newTestConn(db, &fakeProvider{result: reconcileResult(true, false, events...)}, &recordNotifier{})
		c.cycle(context.Background(), opReconcile, false)
	}
	step()                   // merge: reap #1
	step(observed("author")) // reopen: resurrect
	step()                   // re-merge: reap #2

	rm := removals(storedEvents(t, db))
	if len(rm) != 2 {
		t.Fatalf("removals = %d, want 2", len(rm))
	}
	if rm[0].DedupeKey == rm[1].DedupeKey {
		t.Errorf("removal keys must be distinct per episode, both = %q", rm[0].DedupeKey)
	}
	if latestFactOpen(storedEvents(t, db)) {
		t.Error("after re-merge the item should be dropped by the fold")
	}
}

// 8. A startup sweep (nil state) reaps an item that went terminal while the app
// was down — the diff runs the same on the seed reconcile.
func TestReapStartupSweepReaps(t *testing.T) {
	db := openReapDB(t)
	seed(t, db, observed("author"))

	prov := &fakeProvider{result: reconcileResult(true, false)}
	c := newTestConn(db, prov, &recordNotifier{})

	c.cycle(context.Background(), opReconcile, true) // startup=true

	if got := removals(storedEvents(t, db)); len(got) != 1 {
		t.Errorf("startup sweep removals = %d, want 1", len(got))
	}
}

// A store read failure during reap is treated like any storage failure: nothing
// is persisted and the cycle logs a storage outcome.
func TestReapStoreReadErrorIsStorageFailure(t *testing.T) {
	store := &fakeStore{factsErr: context.DeadlineExceeded}
	prov := &fakeProvider{result: reconcileResult(true, false, sdk.Event{NativeID: "x", EventType: eventItemObserved})}
	c := newTestConn(store, prov, &recordNotifier{})

	c.cycle(context.Background(), opReconcile, false)

	if store.lastLog(t).Outcome != outcomeStorage {
		t.Error("a LatestItemFacts failure should log a storage outcome")
	}
	if len(store.eventsWritten) != 0 {
		t.Error("nothing should be written when the reap read fails")
	}
}
