package attention

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/vilaca/devpit/internal/storage"
	"github.com/vilaca/devpit/sdk"
)

// baseTime is a fixed reference "now" for deterministic staleness tests.
var baseTime = time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC)

// obs builds an item.observed StoredEvent for the given identity and facts.
func obs(id int64, conn, native string, facts sdk.ItemObservedPayload) storage.StoredEvent {
	payload, err := json.Marshal(facts)
	if err != nil {
		panic(err)
	}
	return storage.StoredEvent{
		ID:           id,
		ConnectionID: conn,
		ObjectType:   "merge_request",
		NativeID:     native,
		EventType:    eventItemObserved,
		DedupeKey:    "obs",
		Payload:      payload,
		ObservedAt:   baseTime,
	}
}

// sig builds a signal StoredEvent under connection "c" for the given item.
func sig(id int64, native, eventType string, occurredAt time.Time) storage.StoredEvent {
	return storage.StoredEvent{
		ID:           id,
		ConnectionID: "c",
		ObjectType:   "merge_request",
		NativeID:     native,
		EventType:    eventType,
		DedupeKey:    eventType,
		OccurredAt:   &occurredAt,
		Payload:      json.RawMessage(`{}`),
		ObservedAt:   baseTime,
	}
}

// openFacts returns a minimal open snapshot; callers tweak fields per case.
func openFacts() sdk.ItemObservedPayload {
	return sdk.ItemObservedPayload{
		Title:             "T",
		URL:               "https://x/1",
		Repo:              "acme/api",
		State:             "open",
		Author:            "jdoe",
		ProviderUpdatedAt: baseTime.Format(time.RFC3339),
	}
}

func fold(events []storage.StoredEvent) []WorkItem {
	return Fold(events, baseTime, DefaultStaleThreshold, DefaultOldThreshold)
}

func TestFoldStateConditions(t *testing.T) {
	cases := []struct {
		name  string
		facts func(f sdk.ItemObservedPayload) sdk.ItemObservedPayload
		want  []State
	}{
		{
			name: "needs review",
			facts: func(f sdk.ItemObservedPayload) sdk.ItemObservedPayload {
				f.MyRoles = []string{"reviewer"}
				f.MyReviewState = "requested"
				return f
			},
			want: []State{StateReviewRequested},
		},
		{
			name: "changes requested",
			facts: func(f sdk.ItemObservedPayload) sdk.ItemObservedPayload {
				f.MyRoles = []string{"author"}
				f.ReviewDecision = "changes_requested"
				return f
			},
			want: []State{StateChangesRequested},
		},
		{
			name: "blocked",
			facts: func(f sdk.ItemObservedPayload) sdk.ItemObservedPayload {
				f.MyRoles = []string{"author"}
				f.Gate = "blocked"
				return f
			},
			want: []State{StateBlocked},
		},
		{
			name: "ready to merge",
			facts: func(f sdk.ItemObservedPayload) sdk.ItemObservedPayload {
				f.MyRoles = []string{"author"}
				f.Gate = "ready"
				return f
			},
			want: []State{StateReadyToMerge},
		},
		{
			name: "waiting on author",
			facts: func(f sdk.ItemObservedPayload) sdk.ItemObservedPayload {
				f.MyRoles = []string{"reviewer"}
				f.MyReviewState = "changes_requested"
				return f
			},
			want: []State{StateReviewSubmitted},
		},
		{
			name: "draft author is neither blocked nor ready but still shows",
			facts: func(f sdk.ItemObservedPayload) sdk.ItemObservedPayload {
				f.MyRoles = []string{"author"}
				f.Draft = true
				f.Gate = "blocked"
				return f
			},
			want: []State{}, // present, no state tag (Draft marker carries it)
		},
		{
			name: "gate unknown, author, not draft: checking backstop fires",
			facts: func(f sdk.ItemObservedPayload) sdk.ItemObservedPayload {
				f.MyRoles = []string{"author"}
				f.Gate = "unknown"
				return f
			},
			want: []State{StateChecking},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			items := fold([]storage.StoredEvent{obs(1, "c", "acme/api#1", c.facts(openFacts()))})
			if c.want == nil {
				if len(items) != 0 {
					t.Fatalf("want item dropped, got %d items with states %v", len(items), items[0].States)
				}
				return
			}
			if len(items) != 1 {
				t.Fatalf("want 1 item, got %d", len(items))
			}
			if !equalStates(items[0].States, c.want) {
				t.Errorf("states = %v, want %v", items[0].States, c.want)
			}
		})
	}
}

func TestFoldMentionedFromSignal(t *testing.T) {
	events := []storage.StoredEvent{
		obs(1, "c", "acme/api#1", openFacts()), // open, no role -> no fact state
		sig(2, "acme/api#1", signalMentioned, baseTime),
	}
	items := fold(events)
	if len(items) != 1 {
		t.Fatalf("want 1 item, got %d", len(items))
	}
	if !equalStates(items[0].States, []State{StateMentioned}) {
		t.Errorf("states = %v, want [mentioned]", items[0].States)
	}
}

func TestFoldExposesMyRoles(t *testing.T) {
	// my_roles is projected onto the WorkItem so the client can fold reviewer
	// items into the "mentioned" filter even before a review is submitted (the
	// requested-but-not-reviewed case, where my_review_state is still empty).
	f := openFacts()
	f.MyRoles = []string{"reviewer"}
	items := fold([]storage.StoredEvent{obs(1, "c", "acme/api#1", f)})
	if len(items) != 1 {
		t.Fatalf("want 1 item, got %d", len(items))
	}
	if len(items[0].MyRoles) != 1 || items[0].MyRoles[0] != "reviewer" {
		t.Errorf("my_roles = %v, want [reviewer]", items[0].MyRoles)
	}
}

func TestFoldMultipleStatesInPrecedenceOrder(t *testing.T) {
	f := openFacts()
	f.MyRoles = []string{"author"}
	f.ReviewDecision = "changes_requested" // changes_requested
	f.Gate = "blocked"                     // blocked (co-occurs)
	events := []storage.StoredEvent{
		obs(1, "c", "acme/api#1", f),
		sig(2, "acme/api#1", signalMentioned, baseTime), // mentioned
	}
	items := fold(events)
	if len(items) != 1 {
		t.Fatalf("want 1 item, got %d", len(items))
	}
	// changes_requested (#1) < blocked (#3) < mentioned (#4).
	want := []State{StateChangesRequested, StateBlocked, StateMentioned}
	if !equalStates(items[0].States, want) {
		t.Errorf("states = %v, want %v", items[0].States, want)
	}
}

func TestFoldDropsClosedItem(t *testing.T) {
	f := openFacts()
	f.State = "merged"
	f.MyRoles = []string{"author"}
	f.Gate = "ready"
	items := fold([]storage.StoredEvent{obs(1, "c", "acme/api#1", f)})
	if len(items) != 0 {
		t.Fatalf("merged item should be dropped, got %d", len(items))
	}
}

func TestFoldUsesLatestSnapshot(t *testing.T) {
	old := openFacts()
	old.MyRoles = []string{"reviewer"}
	old.MyReviewState = "requested" // would be review_requested

	newer := openFacts()
	newer.MyRoles = []string{"reviewer"}
	newer.MyReviewState = "approved" // now review_submitted

	// Deliberately out of order to prove the fold picks by max ID, not slice order.
	events := []storage.StoredEvent{
		obs(5, "c", "acme/api#1", newer),
		obs(2, "c", "acme/api#1", old),
	}
	items := fold(events)
	if len(items) != 1 {
		t.Fatalf("want 1 item, got %d", len(items))
	}
	if !equalStates(items[0].States, []State{StateReviewSubmitted}) {
		t.Errorf("states = %v, want [review_submitted] (latest snapshot)", items[0].States)
	}
}

func TestFoldRemovedAfterSnapshotDropsItem(t *testing.T) {
	f := openFacts()
	f.MyRoles = []string{"reviewer"}
	f.MyReviewState = "requested"
	events := []storage.StoredEvent{
		obs(1, "c", "acme/api#1", f),
		{ID: 2, ConnectionID: "c", ObjectType: "merge_request", NativeID: "acme/api#1",
			EventType: eventItemRemoved, DedupeKey: "removed", Payload: json.RawMessage(`{}`), ObservedAt: baseTime},
	}
	if items := fold(events); len(items) != 0 {
		t.Fatalf("removed item should be dropped, got %d", len(items))
	}
}

func TestFoldReappearanceAfterRemovalResumes(t *testing.T) {
	f := openFacts()
	f.MyRoles = []string{"reviewer"}
	f.MyReviewState = "requested"
	events := []storage.StoredEvent{
		obs(1, "c", "acme/api#1", f),
		{ID: 2, ConnectionID: "c", ObjectType: "merge_request", NativeID: "acme/api#1",
			EventType: eventItemRemoved, DedupeKey: "removed", Payload: json.RawMessage(`{}`), ObservedAt: baseTime},
		obs(3, "c", "acme/api#1", f), // reappears with a newer snapshot
	}
	if items := fold(events); len(items) != 1 {
		t.Fatalf("reappeared item should be live, got %d", len(items))
	}
}

// TestFoldSaltedResurrectionOrdering pins the exit-side of ADR-0024: after the
// engine reaps an item (observed → removed) and later salts a resurrection, the
// resurrected snapshot carries a *higher id than the removal* even though its
// facts are identical to the pre-removal snapshot. The fold compares by id, so
// the higher-id observed supersedes the removal and the item is visible again.
// Complements TestFoldRemovedAfterSnapshotDropsItem (removal wins when it is the
// higher id).
func TestFoldSaltedResurrectionOrdering(t *testing.T) {
	f := openFacts()
	f.MyRoles = []string{"author"}
	events := []storage.StoredEvent{
		obs(10, "c", "acme/api#1", f), // episode 1 snapshot
		{ID: 20, ConnectionID: "c", ObjectType: "merge_request", NativeID: "acme/api#1",
			EventType: eventItemRemoved, DedupeKey: "item.removed:10", Payload: json.RawMessage(`{}`), ObservedAt: baseTime},
		obs(30, "c", "acme/api#1", f), // salted resurrection: identical facts, higher id
	}
	items := fold(events)
	if len(items) != 1 {
		t.Fatalf("resurrected item should be visible, got %d items", len(items))
	}
	if items[0].NativeID != "acme/api#1" || len(items[0].MyRoles) == 0 {
		t.Errorf("resurrected item = %+v, want acme/api#1 with its role restored", items[0])
	}
}

func TestFoldRankingOrder(t *testing.T) {
	// Within an age band, ranking is pure recency — signal precedence no longer
	// orders items. A newer review_submitted (#9, lowest precedence, and muted)
	// outranks an older review_requested (#2, higher precedence).
	nr := openFacts()
	nr.MyRoles = []string{"reviewer"}
	nr.MyReviewState = "requested" // review_requested (#2), older

	woa := openFacts()
	woa.MyRoles = []string{"reviewer"}
	woa.MyReviewState = "approved" // review_submitted (#9), newer + muted

	events := []storage.StoredEvent{
		obs(1, "c", "acme/api#nr", nr),
		sig(2, "acme/api#nr", signalMentioned, baseTime.Add(-3*time.Hour)),
		obs(3, "c", "acme/api#woa", woa),
		sig(4, "acme/api#woa", signalMentioned, baseTime.Add(-1*time.Hour)),
	}
	items := fold(events)
	if len(items) != 2 {
		t.Fatalf("want 2 items, got %d", len(items))
	}
	if items[0].NativeID != "acme/api#woa" {
		t.Errorf("newer item should rank first regardless of signal precedence/mute; got %q first", items[0].NativeID)
	}
}

func TestFoldNewestFirstWithinBucket(t *testing.T) {
	f := openFacts()
	f.MyRoles = []string{"reviewer"}
	f.MyReviewState = "requested"

	older := baseTime.Add(-2 * time.Hour)
	newer := baseTime.Add(-1 * time.Hour)
	events := []storage.StoredEvent{
		obs(1, "c", "acme/api#old", f),
		sig(2, "acme/api#old", signalMentioned, older),
		obs(3, "c", "acme/api#new", f),
		sig(4, "acme/api#new", signalMentioned, newer),
	}
	items := fold(events)
	if len(items) != 2 {
		t.Fatalf("want 2 items, got %d", len(items))
	}
	if items[0].NativeID != "acme/api#new" {
		t.Errorf("newest item first: got %q then %q", items[0].NativeID, items[1].NativeID)
	}
}

func TestFoldSignalCountsAndRankingTime(t *testing.T) {
	f := openFacts()
	f.MyRoles = []string{"reviewer"}
	f.MyReviewState = "requested"

	t1 := baseTime.Add(-3 * time.Hour)
	t2 := baseTime.Add(-1 * time.Hour) // newest signal
	events := []storage.StoredEvent{
		obs(1, "c", "acme/api#1", f),
		sig(2, "acme/api#1", signalMentioned, t1),
		sig(3, "acme/api#1", signalMentioned, t2),
		sig(4, "acme/api#1", "signal.assigned", t1), // count 1 -> omitted
	}
	items := fold(events)
	if len(items) != 1 {
		t.Fatalf("want 1 item, got %d", len(items))
	}
	item := items[0]
	if item.SignalCounts["mentioned"] != 2 {
		t.Errorf("mentioned count = %d, want 2", item.SignalCounts["mentioned"])
	}
	if _, present := item.SignalCounts["assigned"]; present {
		t.Errorf("single-occurrence signal should be omitted from counts: %v", item.SignalCounts)
	}
	if !item.UpdatedAt.Equal(t2) {
		t.Errorf("UpdatedAt = %v, want newest signal %v", item.UpdatedAt, t2)
	}
}

func TestFoldRankingTimeFallsBackToProviderUpdatedAt(t *testing.T) {
	f := openFacts()
	f.MyRoles = []string{"reviewer"}
	f.MyReviewState = "requested"
	provTime := baseTime.Add(-5 * time.Hour)
	f.ProviderUpdatedAt = provTime.Format(time.RFC3339)

	items := fold([]storage.StoredEvent{obs(1, "c", "acme/api#1", f)}) // no signals
	if len(items) != 1 {
		t.Fatalf("want 1 item, got %d", len(items))
	}
	if !items[0].UpdatedAt.Equal(provTime) {
		t.Errorf("UpdatedAt = %v, want provider_updated_at %v", items[0].UpdatedAt, provTime)
	}
}

func TestFoldStaleBadge(t *testing.T) {
	f := openFacts()
	f.MyRoles = []string{"reviewer"}
	f.MyReviewState = "requested"

	stale := sig(2, "acme/api#stale", signalMentioned, baseTime.Add(-10*24*time.Hour))
	fresh := sig(4, "acme/api#fresh", signalMentioned, baseTime.Add(-1*time.Hour))
	events := []storage.StoredEvent{
		obs(1, "c", "acme/api#stale", f), stale,
		obs(3, "c", "acme/api#fresh", f), fresh,
	}
	items := fold(events)
	byID := map[string]WorkItem{}
	for _, it := range items {
		byID[it.NativeID] = it
	}
	if !byID["acme/api#stale"].Stale {
		t.Error("item older than threshold should be stale")
	}
	if byID["acme/api#fresh"].Stale {
		t.Error("recent item should not be stale")
	}
}

func TestFoldItemID(t *testing.T) {
	f := openFacts()
	f.MyRoles = []string{"reviewer"}
	f.MyReviewState = "requested"
	items := fold([]storage.StoredEvent{obs(1, "gh-personal", "acme/api#412", f)})
	if len(items) != 1 {
		t.Fatalf("want 1 item, got %d", len(items))
	}
	id := items[0].ID
	if len(id) != 16 {
		t.Errorf("id = %q, want 16 hex chars", id)
	}
	// Stable across calls.
	again := fold([]storage.StoredEvent{obs(9, "gh-personal", "acme/api#412", f)})
	if again[0].ID != id {
		t.Errorf("id not stable: %q vs %q", id, again[0].ID)
	}
	// Distinct connections with the same native_id get distinct IDs.
	other := fold([]storage.StoredEvent{obs(1, "gh-work", "acme/api#412", f)})
	if other[0].ID == id {
		t.Error("distinct connection should yield distinct id")
	}
}

func TestFoldGroupsAcrossConnections(t *testing.T) {
	f := openFacts()
	f.MyRoles = []string{"reviewer"}
	f.MyReviewState = "requested"
	events := []storage.StoredEvent{
		obs(1, "c1", "acme/api#1", f),
		obs(2, "c2", "acme/api#1", f), // same native_id, different connection
	}
	items := fold(events)
	if len(items) != 2 {
		t.Fatalf("same native_id under two connections must be two items, got %d", len(items))
	}
}

func TestFoldEmptyInput(t *testing.T) {
	if items := fold(nil); len(items) != 0 {
		t.Fatalf("empty input should yield no items, got %d", len(items))
	}
}

func TestPinLiftsFlaggedInFlagOrder(t *testing.T) {
	// Three auto-ranked items; flag the 3rd then the 1st.
	items := []WorkItem{
		{ID: "a", States: []State{StateReviewRequested}},
		{ID: "b", States: []State{StateBlocked}},
		{ID: "c", States: []State{StateMentioned}},
	}
	pinned := []storage.PinnedItem{{ID: "c"}, {ID: "a"}} // flag order: c before a
	got := pin(items, pinned)

	if len(got) != 3 {
		t.Fatalf("want 3 items, got %d", len(got))
	}
	if got[0].ID != "c" || got[1].ID != "a" {
		t.Errorf("pinned zone = %q,%q; want c,a (flag order)", got[0].ID, got[1].ID)
	}
	if !got[0].Flagged || !got[1].Flagged {
		t.Error("pinned items must be marked Flagged")
	}
	if got[2].ID != "b" || got[2].Flagged {
		t.Errorf("unpinned item = %q flagged=%v; want b flagged=false", got[2].ID, got[2].Flagged)
	}
}

func TestPinNoFlagsIsIdentity(t *testing.T) {
	items := []WorkItem{{ID: "a", States: []State{StateReviewRequested}}}
	got := pin(items, nil)
	if len(got) != 1 || got[0].Flagged {
		t.Errorf("no flags should leave items untouched, got %+v", got)
	}
}

func TestPinIgnoresFlagWithNoLiveItem(t *testing.T) {
	items := []WorkItem{{ID: "a", States: []State{StateReviewRequested}}}
	got := pin(items, []storage.PinnedItem{{ID: "gone"}, {ID: "a"}})
	if len(got) != 1 || got[0].ID != "a" || !got[0].Flagged {
		t.Errorf("stale flag should be ignored, got %+v", got)
	}
}

func TestListReadsFoldsAndPins(t *testing.T) {
	db, err := storage.Open(":memory:")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = db.Close() }()
	ctx := context.Background()

	// Two authored items: one ready_to_merge, one review_requested.
	nr := openFacts()
	nr.MyRoles = []string{"reviewer"}
	nr.MyReviewState = "requested"

	rtm := openFacts()
	rtm.MyRoles = []string{"author"}
	rtm.Gate = "ready"

	events := []sdk.Event{
		{ObjectType: "merge_request", NativeID: "acme/api#nr", EventType: eventItemObserved, DedupeKey: "nr", Payload: nr},
		{ObjectType: "merge_request", NativeID: "acme/api#rtm", EventType: eventItemObserved, DedupeKey: "rtm", Payload: rtm},
	}
	if _, err := db.WriteEvents(ctx, "c", events); err != nil {
		t.Fatalf("WriteEvents: %v", err)
	}

	// Pin the ready_to_merge item, which would otherwise rank below review_requested.
	rtmID := itemID(itemKey{"c", "merge_request", "acme/api#rtm"})
	if err := db.SetHandleNext(ctx, rtmID, true); err != nil {
		t.Fatalf("SetHandleNext: %v", err)
	}

	items, err := List(ctx, db, []string{"c"}, time.Now(), DefaultStaleThreshold, DefaultOldThreshold)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("want 2 items, got %d", len(items))
	}
	if items[0].NativeID != "acme/api#rtm" || !items[0].Flagged {
		t.Errorf("pinned ready_to_merge should lead; got %q flagged=%v", items[0].NativeID, items[0].Flagged)
	}
	if items[1].NativeID != "acme/api#nr" || items[1].Flagged {
		t.Errorf(
			"auto-ranked review_requested should follow unflagged; got %q flagged=%v",
			items[1].NativeID, items[1].Flagged)
	}
}

// --- Marker field tests ---

func TestFoldMarkersFromFacts(t *testing.T) {
	cases := []struct {
		name         string
		mutate       func(*sdk.ItemObservedPayload)
		wantFailing  bool
		wantConflict bool
		wantRebase   bool
		wantDetail   string
	}{
		{
			name:        "failing checks",
			mutate:      func(f *sdk.ItemObservedPayload) { f.FailingChecks = true; f.GateDetail = "unstable" },
			wantFailing: true, wantDetail: "unstable",
		},
		{
			name: "merge conflict",
			mutate: func(f *sdk.ItemObservedPayload) {
				f.MergeConflict = true
				f.Gate = "blocked"
				f.GateDetail = "dirty"
			},
			wantConflict: true, wantDetail: "dirty",
		},
		{
			name:       "needs rebase",
			mutate:     func(f *sdk.ItemObservedPayload) { f.NeedsRebase = true; f.Gate = "blocked"; f.GateDetail = "behind" },
			wantRebase: true, wantDetail: "behind",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := openFacts()
			f.MyRoles = []string{"author"}
			f.Gate = "ready" // ensure item has a state
			c.mutate(&f)
			items := fold([]storage.StoredEvent{obs(1, "c", "acme/api#1", f)})
			if len(items) != 1 {
				t.Fatalf("want 1 item, got %d", len(items))
			}
			it := items[0]
			if it.FailingChecks != c.wantFailing {
				t.Errorf("failing_checks = %v, want %v", it.FailingChecks, c.wantFailing)
			}
			if it.MergeConflict != c.wantConflict {
				t.Errorf("merge_conflict = %v, want %v", it.MergeConflict, c.wantConflict)
			}
			if it.NeedsRebase != c.wantRebase {
				t.Errorf("needs_rebase = %v, want %v", it.NeedsRebase, c.wantRebase)
			}
			if it.GateDetail != c.wantDetail {
				t.Errorf("gate_detail = %q, want %q", it.GateDetail, c.wantDetail)
			}
		})
	}
}

// --- Age tier tests ---

func TestAgeTierExclusivity(t *testing.T) {
	f := openFacts()
	f.MyRoles = []string{"reviewer"}
	f.MyReviewState = "requested"

	fresh := sig(2, "acme/api#fresh", signalMentioned, baseTime.Add(-1*time.Hour))
	stale10 := sig(4, "acme/api#stale", signalMentioned, baseTime.Add(-10*24*time.Hour))
	aband40 := sig(6, "acme/api#old", signalMentioned, baseTime.Add(-40*24*time.Hour))

	events := []storage.StoredEvent{
		obs(1, "c", "acme/api#fresh", f), fresh,
		obs(3, "c", "acme/api#stale", f), stale10,
		obs(5, "c", "acme/api#old", f), aband40,
	}
	items := fold(events)
	byID := map[string]WorkItem{}
	for _, it := range items {
		byID[it.NativeID] = it
	}

	freshItem := byID["acme/api#fresh"]
	if freshItem.Stale || freshItem.Old {
		t.Errorf("fresh item: stale=%v old=%v, want both false", freshItem.Stale, freshItem.Old)
	}

	staleItem := byID["acme/api#stale"]
	if !staleItem.Stale || staleItem.Old {
		t.Errorf("10-day item: stale=%v old=%v, want stale=true old=false", staleItem.Stale, staleItem.Old)
	}

	oldItem := byID["acme/api#old"]
	if oldItem.Stale || !oldItem.Old {
		t.Errorf("40-day item: stale=%v old=%v, want stale=false old=true", oldItem.Stale, oldItem.Old)
	}
}

func TestAgeTierDisabledThresholds(t *testing.T) {
	f := openFacts()
	f.MyRoles = []string{"reviewer"}
	f.MyReviewState = "requested"

	// Item 40 days old with old threshold disabled: should be stale, not old.
	stale40 := sig(2, "acme/api#1", signalMentioned, baseTime.Add(-40*24*time.Hour))
	events := []storage.StoredEvent{obs(1, "c", "acme/api#1", f), stale40}

	items := Fold(events, baseTime, DefaultStaleThreshold, 0 /* old disabled */)
	if len(items) != 1 {
		t.Fatalf("want 1 item, got %d", len(items))
	}
	it := items[0]
	if !it.Stale {
		t.Error("40-day item should be stale when old threshold is disabled")
	}
	if it.Old {
		t.Error("old should be false when threshold is 0")
	}
}

// --- Age band sorting tests ---

func TestAgeBandSorting(t *testing.T) {
	// Age band is the top-level sort key: a fresh item sorts above a stale one
	// even though, within a band, order is pure recency (no signal precedence).
	freshFacts := openFacts()
	freshFacts.MyRoles = []string{"reviewer"} // mentioned only (#4)

	staleFacts := openFacts()
	staleFacts.MyRoles = []string{"reviewer"}
	staleFacts.MyReviewState = "requested" // review_requested (#2)

	freshSig := sig(2, "acme/api#fresh", signalMentioned, baseTime.Add(-1*time.Hour))
	staleSig := sig(4, "acme/api#stale", signalMentioned, baseTime.Add(-10*24*time.Hour))

	events := []storage.StoredEvent{
		obs(1, "c", "acme/api#stale", staleFacts), staleSig,
		obs(3, "c", "acme/api#fresh", freshFacts), freshSig,
	}
	items := fold(events)
	if len(items) != 2 {
		t.Fatalf("want 2 items, got %d", len(items))
	}
	if items[0].NativeID != "acme/api#fresh" {
		t.Errorf("fresh mentioned should rank above stale review_requested; got %q first", items[0].NativeID)
	}
}

func TestReviewedDoneMuteDoesNotReorder(t *testing.T) {
	// Muting is a display cue only: it no longer moves an item. A muted
	// reviewed-done item that updated more recently still outranks a non-muted
	// one in the same age band, purely on recency.
	done := openFacts()
	done.MyRoles = []string{"reviewer"}
	done.MyReviewState = "approved"
	doneSig := sig(2, "acme/api#done", signalMentioned, baseTime.Add(-1*time.Hour)) // newer

	active := openFacts()
	active.MyRoles = []string{"reviewer"}
	active.MyReviewState = "requested" // review_requested, older
	activeSig := sig(4, "acme/api#active", signalMentioned, baseTime.Add(-2*time.Hour))

	events := []storage.StoredEvent{
		obs(1, "c", "acme/api#done", done), doneSig,
		obs(3, "c", "acme/api#active", active), activeSig,
	}
	items := fold(events)
	if len(items) != 2 {
		t.Fatalf("want 2 items, got %d", len(items))
	}
	if items[0].NativeID != "acme/api#done" {
		t.Errorf("newer item should rank first regardless of muting; got %q first", items[0].NativeID)
	}
	if !items[0].Muted {
		t.Error("reviewed-done item should still carry Muted=true")
	}
	if items[1].Muted {
		t.Error("active review_requested item should not be Muted")
	}
}

func TestAuthorNeverMuted(t *testing.T) {
	// An authored item is never muted, even if a review by the user is recorded.
	f := openFacts()
	f.MyRoles = []string{"author", "reviewer"}
	f.MyReviewState = "approved"
	f.Gate = "ready"
	items := fold([]storage.StoredEvent{obs(1, "c", "acme/api#1", f)})
	if len(items) != 1 {
		t.Fatalf("want 1 item, got %d", len(items))
	}
	if items[0].Muted {
		t.Error("an authored item must never be muted")
	}
}

func TestAgeBandOldLast(t *testing.T) {
	f := openFacts()
	f.MyRoles = []string{"reviewer"}
	f.MyReviewState = "requested" // review_requested for all

	freshSig := sig(2, "acme/api#fresh", signalMentioned, baseTime.Add(-1*time.Hour))
	staleSig := sig(4, "acme/api#stale", signalMentioned, baseTime.Add(-10*24*time.Hour))
	oldSig := sig(6, "acme/api#old", signalMentioned, baseTime.Add(-40*24*time.Hour))

	events := []storage.StoredEvent{
		obs(1, "c", "acme/api#fresh", f), freshSig,
		obs(3, "c", "acme/api#stale", f), staleSig,
		obs(5, "c", "acme/api#old", f), oldSig,
	}
	items := fold(events)
	if len(items) != 3 {
		t.Fatalf("want 3 items, got %d", len(items))
	}
	if items[0].NativeID != "acme/api#fresh" ||
		items[1].NativeID != "acme/api#stale" ||
		items[2].NativeID != "acme/api#old" {
		t.Errorf("band order wrong: %q %q %q",
			items[0].NativeID, items[1].NativeID, items[2].NativeID)
	}
}

func TestAgeBandWithinBandOrderPreserved(t *testing.T) {
	// Two review_requested items, both fresh; newer should sort first (existing order).
	f := openFacts()
	f.MyRoles = []string{"reviewer"}
	f.MyReviewState = "requested"

	newerSig := sig(2, "acme/api#newer", signalMentioned, baseTime.Add(-1*time.Hour))
	olderSig := sig(4, "acme/api#older", signalMentioned, baseTime.Add(-3*time.Hour))

	events := []storage.StoredEvent{
		obs(1, "c", "acme/api#older", f), olderSig,
		obs(3, "c", "acme/api#newer", f), newerSig,
	}
	items := fold(events)
	if len(items) != 2 {
		t.Fatalf("want 2 items, got %d", len(items))
	}
	if items[0].NativeID != "acme/api#newer" {
		t.Errorf("within band: newer item should rank first; got %q", items[0].NativeID)
	}
}

func TestPinnedItemsIgnoreAgeBand(t *testing.T) {
	// A pinned old item should stay pinned at the top (bands don't reorder pins).
	f := openFacts()
	f.MyRoles = []string{"reviewer"}
	f.MyReviewState = "requested"

	freshSig := sig(2, "acme/api#fresh", signalMentioned, baseTime.Add(-1*time.Hour))
	oldSig2 := sig(4, "acme/api#old", signalMentioned, baseTime.Add(-40*24*time.Hour))

	ranked := Fold([]storage.StoredEvent{
		obs(1, "c", "acme/api#fresh", f), freshSig,
		obs(3, "c", "acme/api#old", f), oldSig2,
	}, baseTime, DefaultStaleThreshold, DefaultOldThreshold)

	oldID := itemID(itemKey{"c", "merge_request", "acme/api#old"})
	pinned := pin(ranked, []storage.PinnedItem{{ID: oldID}})

	if len(pinned) != 2 {
		t.Fatalf("want 2 items, got %d", len(pinned))
	}
	if pinned[0].ID != oldID || !pinned[0].Flagged {
		t.Errorf("old pinned item should be first; got %q flagged=%v", pinned[0].ID, pinned[0].Flagged)
	}
}

// --- Pin age (FlaggedAt) tests ---

func TestPinFlaggedAtSurfaces(t *testing.T) {
	flagTime := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	items := []WorkItem{{ID: "a", States: []State{StateReviewRequested}}}
	pinned := []storage.PinnedItem{{ID: "a", FlaggedAt: flagTime}}
	got := pin(items, pinned)
	if len(got) != 1 {
		t.Fatalf("want 1 item, got %d", len(got))
	}
	if got[0].FlaggedAt == nil || !got[0].FlaggedAt.Equal(flagTime) {
		t.Errorf("FlaggedAt = %v, want %v", got[0].FlaggedAt, flagTime)
	}
}

// --- Onset (Since) tests ---

func TestOnsetContiguousRun(t *testing.T) {
	// Three snapshots where review_requested is true throughout → onset = oldest.
	f := openFacts()
	f.MyRoles = []string{"reviewer"}
	f.MyReviewState = "requested"

	t3 := baseTime.Add(-3 * time.Hour) // oldest
	t2 := baseTime.Add(-2 * time.Hour)
	t1 := baseTime.Add(-1 * time.Hour) // newest (used as ProviderUpdatedAt)
	f1, f2, f3 := f, f, f
	f1.ProviderUpdatedAt = t1.Format(time.RFC3339)
	f2.ProviderUpdatedAt = t2.Format(time.RFC3339)
	f3.ProviderUpdatedAt = t3.Format(time.RFC3339)

	events := []storage.StoredEvent{
		obs(3, "c", "acme/api#1", f1),
		obs(2, "c", "acme/api#1", f2),
		obs(1, "c", "acme/api#1", f3),
	}
	items := fold(events)
	if len(items) != 1 {
		t.Fatalf("want 1 item, got %d", len(items))
	}
	onset, ok := items[0].Since[string(StateReviewRequested)]
	if !ok {
		t.Fatal("review_requested should have onset in Since map")
	}
	if !onset.Equal(t3) {
		t.Errorf("onset = %v, want %v (oldest in run)", onset, t3)
	}
}

func TestOnsetBrokenRun(t *testing.T) {
	// State false in middle snapshot → onset = latest run only.
	f := openFacts()
	f.MyRoles = []string{"reviewer"}
	f.MyReviewState = "requested"

	fOff := openFacts() // state off in snapshot 2
	fOff.MyRoles = []string{"reviewer"}
	fOff.MyReviewState = "approved"

	t3 := baseTime.Add(-3 * time.Hour)
	t2 := baseTime.Add(-2 * time.Hour)
	t1 := baseTime.Add(-1 * time.Hour)
	f1, f2, f3 := f, fOff, f
	f1.ProviderUpdatedAt = t1.Format(time.RFC3339)
	f2.ProviderUpdatedAt = t2.Format(time.RFC3339)
	f3.ProviderUpdatedAt = t3.Format(time.RFC3339)

	events := []storage.StoredEvent{
		obs(3, "c", "acme/api#1", f1), // newest: state on
		obs(2, "c", "acme/api#1", f2), // middle: state off — breaks run
		obs(1, "c", "acme/api#1", f3), // oldest: state on but excluded
	}
	items := fold(events)
	if len(items) != 1 {
		t.Fatalf("want 1 item, got %d", len(items))
	}
	onset, ok := items[0].Since[string(StateReviewRequested)]
	if !ok {
		t.Fatal("review_requested should have onset in Since map")
	}
	// Run is only snapshot 3 (newest), so onset = t1.
	if !onset.Equal(t1) {
		t.Errorf("onset = %v, want %v (only latest run)", onset, t1)
	}
}

func TestOnsetMentioned(t *testing.T) {
	f := openFacts()
	f.MyRoles = []string{"reviewer"}
	f.MyReviewState = "requested"

	earlyMention := sig(2, "acme/api#1", signalMentioned, baseTime.Add(-5*time.Hour))
	lateMention := sig(3, "acme/api#1", signalMentioned, baseTime.Add(-1*time.Hour))

	events := []storage.StoredEvent{obs(1, "c", "acme/api#1", f), earlyMention, lateMention}
	items := fold(events)
	if len(items) != 1 {
		t.Fatalf("want 1 item, got %d", len(items))
	}
	onset, ok := items[0].Since["mentioned"]
	if !ok {
		t.Fatal("mentioned should have onset in Since map")
	}
	// Onset = earliest mention signal.
	if !onset.Equal(baseTime.Add(-5 * time.Hour)) {
		t.Errorf("onset = %v, want earliest mention time", onset)
	}
}

func TestOnsetMarker(t *testing.T) {
	// failing_checks true across 2 snapshots → onset = oldest.
	f := openFacts()
	f.MyRoles = []string{"author"}
	f.Gate = "ready"
	f.FailingChecks = true

	t2 := baseTime.Add(-2 * time.Hour)
	t1 := baseTime.Add(-1 * time.Hour)
	f1, f2 := f, f
	f1.ProviderUpdatedAt = t1.Format(time.RFC3339)
	f2.ProviderUpdatedAt = t2.Format(time.RFC3339)

	events := []storage.StoredEvent{
		obs(2, "c", "acme/api#1", f1),
		obs(1, "c", "acme/api#1", f2),
	}
	items := fold(events)
	if len(items) != 1 {
		t.Fatalf("want 1 item, got %d", len(items))
	}
	onset, ok := items[0].Since["failing_checks"]
	if !ok {
		t.Fatal("failing_checks should have onset in Since map")
	}
	if !onset.Equal(t2) {
		t.Errorf("onset = %v, want %v (oldest in run)", onset, t2)
	}
}

func TestOnsetAbsentForInactiveTags(t *testing.T) {
	f := openFacts()
	f.MyRoles = []string{"author"}
	f.Gate = "ready"
	// No markers set.
	items := fold([]storage.StoredEvent{obs(1, "c", "acme/api#1", f)})
	if len(items) != 1 {
		t.Fatalf("want 1 item, got %d", len(items))
	}
	for _, key := range []string{"failing_checks", "merge_conflict", "needs_rebase", "draft"} {
		if _, ok := items[0].Since[key]; ok {
			t.Errorf("inactive marker %q should not appear in Since", key)
		}
	}
}

// --- Signal-model (v0.1.5) tests ---

func TestSignalAuthoredMRNeverBare(t *testing.T) {
	// Authored, non-draft, gate unknown → checking (not empty).
	f := openFacts()
	f.MyRoles = []string{"author"}
	f.Gate = "unknown"
	items := fold([]storage.StoredEvent{obs(1, "c", "acme/api#1", f)})
	if len(items) != 1 {
		t.Fatalf("want 1 item, got %d", len(items))
	}
	if !equalStates(items[0].States, []State{StateChecking}) {
		t.Errorf("states = %v, want [checking]", items[0].States)
	}
}

func TestSignalDraftCheckingRoleNeutral(t *testing.T) {
	// Draft with gate unknown → checking fires for both authored and reviewer.
	for _, role := range []string{"author", "reviewer"} {
		t.Run(role, func(t *testing.T) {
			f := openFacts()
			f.MyRoles = []string{role}
			f.Draft = true
			f.Gate = "unknown"
			items := fold([]storage.StoredEvent{obs(1, "c", "acme/api#1", f)})
			if len(items) != 1 {
				t.Fatalf("role=%s: want 1 item, got %d", role, len(items))
			}
			it := items[0]
			if !it.Draft {
				t.Errorf("role=%s: item.draft should be true", role)
			}
			if !equalStates(it.States, []State{StateChecking}) {
				t.Errorf("role=%s: states = %v, want [checking]", role, it.States)
			}
			for _, s := range it.States {
				if s == StateBlocked || s == StateReadyToMerge {
					t.Errorf("role=%s: draft must not carry blocked/ready_to_merge; got %v", role, it.States)
				}
			}
		})
	}
}

func TestSignalAuthorScopePreserved(t *testing.T) {
	// A reviewer on a blocked MR does not get the blocked signal.
	f := openFacts()
	f.MyRoles = []string{"reviewer"}
	f.Gate = "blocked"
	items := fold([]storage.StoredEvent{obs(1, "c", "acme/api#1", f)})
	if len(items) != 1 {
		t.Fatalf("want 1 item, got %d", len(items))
	}
	for _, s := range items[0].States {
		if s == StateBlocked || s == StateReadyToMerge || s == StateAutoMergeArmed || s == StateChecksRunning {
			t.Errorf("reviewer must not carry author-scoped signal %q; states = %v", s, items[0].States)
		}
	}
	// Author on same MR does get blocked.
	f2 := openFacts()
	f2.MyRoles = []string{"author"}
	f2.Gate = "blocked"
	items2 := fold([]storage.StoredEvent{obs(1, "c", "acme/api#2", f2)})
	if len(items2) != 1 {
		t.Fatalf("want 1 authored item, got %d", len(items2))
	}
	found := false
	for _, s := range items2[0].States {
		if s == StateBlocked {
			found = true
		}
	}
	if !found {
		t.Errorf("author on blocked MR must carry blocked; got %v", items2[0].States)
	}
}

func TestSignalNonAuthoredCanBeMarkerOnly(t *testing.T) {
	// Assignee on a ready MR with no mention, no review state → empty states (D2).
	f := openFacts()
	f.MyRoles = []string{"assignee"}
	f.Gate = "ready"
	items := fold([]storage.StoredEvent{obs(1, "c", "acme/api#1", f)})
	if len(items) != 1 {
		t.Fatalf("want 1 item, got %d", len(items))
	}
	if len(items[0].States) != 0 {
		t.Errorf("non-authored assignee on ready MR: states = %v, want []", items[0].States)
	}
}

func TestSignalPrecedenceMentionedAndChecking(t *testing.T) {
	// Authored + mentioned + gate unknown → [mentioned, checking]; ranks by mentioned.
	f := openFacts()
	f.MyRoles = []string{"author"}
	f.Gate = "unknown"
	events := []storage.StoredEvent{
		obs(1, "c", "acme/api#1", f),
		sig(2, "acme/api#1", signalMentioned, baseTime),
	}
	items := fold(events)
	if len(items) != 1 {
		t.Fatalf("want 1 item, got %d", len(items))
	}
	// Chips render in precedence order: mentioned (#4) before checking (#8).
	want := []State{StateMentioned, StateChecking}
	if !equalStates(items[0].States, want) {
		t.Errorf("states = %v, want %v", items[0].States, want)
	}
}

func TestSignalNewSignalsAutoMergeAndChecksRunning(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*sdk.ItemObservedPayload)
		want   State
	}{
		{
			name:   "auto_merge_armed authored non-draft",
			mutate: func(f *sdk.ItemObservedPayload) { f.AutoMergeArmed = true },
			want:   StateAutoMergeArmed,
		},
		{
			name:   "checks_running authored non-draft",
			mutate: func(f *sdk.ItemObservedPayload) { f.ChecksRunning = true },
			want:   StateChecksRunning,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := openFacts()
			f.MyRoles = []string{"author"}
			f.Gate = "ready"
			c.mutate(&f)
			items := fold([]storage.StoredEvent{obs(1, "c", "acme/api#1", f)})
			if len(items) != 1 {
				t.Fatalf("want 1 item, got %d", len(items))
			}
			found := false
			for _, s := range items[0].States {
				if s == c.want {
					found = true
				}
			}
			if !found {
				t.Errorf("want %q in states %v", c.want, items[0].States)
			}

			// Suppressed on draft.
			fd := openFacts()
			fd.MyRoles = []string{"author"}
			fd.Draft = true
			fd.Gate = "unknown"
			c.mutate(&fd)
			draftItems := fold([]storage.StoredEvent{obs(2, "c", "acme/api#draft", fd)})
			for _, s := range draftItems[0].States {
				if s == c.want {
					t.Errorf("draft must not carry %q; states = %v", c.want, draftItems[0].States)
				}
			}

			// Suppressed for reviewer.
			fr := openFacts()
			fr.MyRoles = []string{"reviewer"}
			fr.Gate = "ready"
			c.mutate(&fr)
			revItems := fold([]storage.StoredEvent{obs(3, "c", "acme/api#rev", fr)})
			for _, s := range revItems[0].States {
				if s == c.want {
					t.Errorf("reviewer must not carry %q; states = %v", c.want, revItems[0].States)
				}
			}
		})
	}
}

func TestSignalCheckingOnsetViaComputeSince(t *testing.T) {
	// Three unknown-gate snapshots → since["checking"] = oldest (D8).
	f := openFacts()
	f.MyRoles = []string{"author"}
	f.Gate = "unknown"

	t3 := baseTime.Add(-3 * time.Hour)
	t2 := baseTime.Add(-2 * time.Hour)
	t1 := baseTime.Add(-1 * time.Hour)
	f1, f2, f3 := f, f, f
	f1.ProviderUpdatedAt = t1.Format(time.RFC3339)
	f2.ProviderUpdatedAt = t2.Format(time.RFC3339)
	f3.ProviderUpdatedAt = t3.Format(time.RFC3339)

	events := []storage.StoredEvent{
		obs(3, "c", "acme/api#1", f1),
		obs(2, "c", "acme/api#1", f2),
		obs(1, "c", "acme/api#1", f3),
	}
	items := fold(events)
	if len(items) != 1 {
		t.Fatalf("want 1 item, got %d", len(items))
	}
	onset, ok := items[0].Since[string(StateChecking)]
	if !ok {
		t.Fatal("checking should have onset in Since map")
	}
	if !onset.Equal(t3) {
		t.Errorf("checking onset = %v, want %v (oldest in run)", onset, t3)
	}
}

func TestSoleApproverStates(t *testing.T) {
	t.Run("sole_approver unreviewed gate unknown → review_requested", func(t *testing.T) {
		f := openFacts()
		f.MyRoles = []string{"sole_approver"}
		f.MyReviewState = ""
		f.Gate = "unknown"
		items := fold([]storage.StoredEvent{obs(1, "c", "acme/api#1", f)})
		if len(items) != 1 {
			t.Fatalf("want 1 item, got %d", len(items))
		}
		if !equalStates(items[0].States, []State{StateReviewRequested, StateChecking}) {
			t.Errorf("states = %v, want [review_requested, checking]", items[0].States)
		}
	})

	t.Run("sole_approver reviewed gate ready → ready_to_merge not muted", func(t *testing.T) {
		f := openFacts()
		f.MyRoles = []string{"sole_approver"}
		f.MyReviewState = "approved"
		f.Gate = "ready"
		items := fold([]storage.StoredEvent{obs(1, "c", "acme/api#2", f)})
		if len(items) != 1 {
			t.Fatalf("want 1 item, got %d", len(items))
		}
		found := false
		for _, s := range items[0].States {
			if s == StateReadyToMerge {
				found = true
			}
		}
		if !found {
			t.Errorf("states = %v, want StateReadyToMerge present", items[0].States)
		}
		if items[0].Muted {
			t.Error("sole_approver must not be muted even after reviewing")
		}
	})

	t.Run("sole_approver draft → no review_requested signal", func(t *testing.T) {
		f := openFacts()
		f.MyRoles = []string{"sole_approver"}
		f.Draft = true
		f.Gate = "unknown"
		items := fold([]storage.StoredEvent{obs(1, "c", "acme/api#3", f)})
		if len(items) != 1 {
			t.Fatalf("want 1 item, got %d", len(items))
		}
		for _, s := range items[0].States {
			if s == StateReviewRequested {
				t.Errorf("draft sole_approver must not carry review_requested; states = %v", items[0].States)
			}
		}
	})

	t.Run("reviewer-only reviewed → muted", func(t *testing.T) {
		f := openFacts()
		f.MyRoles = []string{"reviewer"}
		f.MyReviewState = "approved"
		f.Gate = "ready"
		items := fold([]storage.StoredEvent{obs(1, "c", "acme/api#4", f)})
		if len(items) != 1 {
			t.Fatalf("want 1 item, got %d", len(items))
		}
		if !items[0].Muted {
			t.Error("reviewer-only reviewed item must be muted")
		}
	})

	t.Run("reviewer + sole_approver reviewed → NOT muted", func(t *testing.T) {
		f := openFacts()
		f.MyRoles = []string{"reviewer", "sole_approver"}
		f.MyReviewState = "approved"
		f.Gate = "ready"
		items := fold([]storage.StoredEvent{obs(1, "c", "acme/api#5", f)})
		if len(items) != 1 {
			t.Fatalf("want 1 item, got %d", len(items))
		}
		if items[0].Muted {
			t.Error("reviewer+sole_approver must not be muted")
		}
	})
}

func equalStates(a, b []State) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
