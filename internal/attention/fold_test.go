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
	return Fold(events, baseTime, DefaultStaleThreshold)
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
			want: []State{StateNeedsReview},
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
			want: []State{StateWaitingOnAuthor},
		},
		{
			name: "draft author is neither blocked nor ready",
			facts: func(f sdk.ItemObservedPayload) sdk.ItemObservedPayload {
				f.MyRoles = []string{"author"}
				f.Draft = true
				f.Gate = "blocked"
				return f
			},
			want: nil, // dropped: no state
		},
		{
			name: "gate unknown, author, not draft, no review: no state",
			facts: func(f sdk.ItemObservedPayload) sdk.ItemObservedPayload {
				f.MyRoles = []string{"author"}
				f.Gate = "unknown"
				return f
			},
			want: nil,
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
	// changes_requested (rank 1) < blocked (rank 2) < mentioned (rank 4).
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
	old.MyReviewState = "requested" // would be needs_review

	newer := openFacts()
	newer.MyRoles = []string{"reviewer"}
	newer.MyReviewState = "approved" // now waiting_on_author

	// Deliberately out of order to prove the fold picks by max ID, not slice order.
	events := []storage.StoredEvent{
		obs(5, "c", "acme/api#1", newer),
		obs(2, "c", "acme/api#1", old),
	}
	items := fold(events)
	if len(items) != 1 {
		t.Fatalf("want 1 item, got %d", len(items))
	}
	if !equalStates(items[0].States, []State{StateWaitingOnAuthor}) {
		t.Errorf("states = %v, want [waiting_on_author] (latest snapshot)", items[0].States)
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

func TestFoldRankingOrder(t *testing.T) {
	// One item per bucket, all same timestamp, to assert precedence ordering.
	nr := openFacts()
	nr.MyRoles = []string{"reviewer"}
	nr.MyReviewState = "requested"

	rtm := openFacts()
	rtm.MyRoles = []string{"author"}
	rtm.Gate = "ready"

	woa := openFacts()
	woa.MyRoles = []string{"reviewer"}
	woa.MyReviewState = "approved"

	events := []storage.StoredEvent{
		obs(1, "c", "acme/api#woa", woa),
		obs(2, "c", "acme/api#rtm", rtm),
		obs(3, "c", "acme/api#nr", nr),
	}
	items := fold(events)
	if len(items) != 3 {
		t.Fatalf("want 3 items, got %d", len(items))
	}
	gotOrder := []State{items[0].States[0], items[1].States[0], items[2].States[0]}
	want := []State{StateNeedsReview, StateReadyToMerge, StateWaitingOnAuthor}
	if !equalStates(gotOrder, want) {
		t.Errorf("ranking order = %v, want %v", gotOrder, want)
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
		{ID: "a", States: []State{StateNeedsReview}},
		{ID: "b", States: []State{StateBlocked}},
		{ID: "c", States: []State{StateMentioned}},
	}
	got := pin(items, []string{"c", "a"}) // flag order: c before a

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
	items := []WorkItem{{ID: "a", States: []State{StateNeedsReview}}}
	got := pin(items, nil)
	if len(got) != 1 || got[0].Flagged {
		t.Errorf("no flags should leave items untouched, got %+v", got)
	}
}

func TestPinIgnoresFlagWithNoLiveItem(t *testing.T) {
	items := []WorkItem{{ID: "a", States: []State{StateNeedsReview}}}
	got := pin(items, []string{"gone", "a"})
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

	// Two authored items: one ready_to_merge, one needs_review.
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

	// Pin the ready_to_merge item, which would otherwise rank below needs_review.
	rtmID := itemID(itemKey{"c", "merge_request", "acme/api#rtm"})
	if err := db.SetHandleNext(ctx, rtmID, true); err != nil {
		t.Fatalf("SetHandleNext: %v", err)
	}

	items, err := List(ctx, db, []string{"c"}, time.Now(), DefaultStaleThreshold)
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
		t.Errorf("auto-ranked needs_review should follow unflagged; got %q flagged=%v", items[1].NativeID, items[1].Flagged)
	}
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
