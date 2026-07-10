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
	return Fold(events, baseTime, DefaultStaleThreshold, DefaultAbandonedThreshold)
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
	items := []WorkItem{{ID: "a", States: []State{StateNeedsReview}}}
	got := pin(items, nil)
	if len(got) != 1 || got[0].Flagged {
		t.Errorf("no flags should leave items untouched, got %+v", got)
	}
}

func TestPinIgnoresFlagWithNoLiveItem(t *testing.T) {
	items := []WorkItem{{ID: "a", States: []State{StateNeedsReview}}}
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

	items, err := List(ctx, db, []string{"c"}, time.Now(), DefaultStaleThreshold, DefaultAbandonedThreshold)
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
			name:         "merge conflict",
			mutate:       func(f *sdk.ItemObservedPayload) { f.MergeConflict = true; f.Gate = "blocked"; f.GateDetail = "dirty" },
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
	aband40 := sig(6, "acme/api#abandoned", signalMentioned, baseTime.Add(-40*24*time.Hour))

	events := []storage.StoredEvent{
		obs(1, "c", "acme/api#fresh", f), fresh,
		obs(3, "c", "acme/api#stale", f), stale10,
		obs(5, "c", "acme/api#abandoned", f), aband40,
	}
	items := fold(events)
	byID := map[string]WorkItem{}
	for _, it := range items {
		byID[it.NativeID] = it
	}

	freshItem := byID["acme/api#fresh"]
	if freshItem.Stale || freshItem.Abandoned {
		t.Errorf("fresh item: stale=%v abandoned=%v, want both false", freshItem.Stale, freshItem.Abandoned)
	}

	staleItem := byID["acme/api#stale"]
	if !staleItem.Stale || staleItem.Abandoned {
		t.Errorf("10-day item: stale=%v abandoned=%v, want stale=true abandoned=false", staleItem.Stale, staleItem.Abandoned)
	}

	abandItem := byID["acme/api#abandoned"]
	if abandItem.Stale || !abandItem.Abandoned {
		t.Errorf("40-day item: stale=%v abandoned=%v, want stale=false abandoned=true", abandItem.Stale, abandItem.Abandoned)
	}
}

func TestAgeTierDisabledThresholds(t *testing.T) {
	f := openFacts()
	f.MyRoles = []string{"reviewer"}
	f.MyReviewState = "requested"

	// Item 40 days old with abandoned threshold disabled: should be stale, not abandoned.
	stale40 := sig(2, "acme/api#1", signalMentioned, baseTime.Add(-40*24*time.Hour))
	events := []storage.StoredEvent{obs(1, "c", "acme/api#1", f), stale40}

	items := Fold(events, baseTime, DefaultStaleThreshold, 0 /* abandoned disabled */)
	if len(items) != 1 {
		t.Fatalf("want 1 item, got %d", len(items))
	}
	it := items[0]
	if !it.Stale {
		t.Error("40-day item should be stale when abandoned threshold is disabled")
	}
	if it.Abandoned {
		t.Error("abandoned should be false when threshold is 0")
	}
}

// --- Age band sorting tests ---

func TestAgeBandSorting(t *testing.T) {
	// stale needs_review sorts below fresh waiting_on_author (lower-ranked state).
	freshFacts := openFacts()
	freshFacts.MyRoles = []string{"reviewer"}
	freshFacts.MyReviewState = "approved" // waiting_on_author

	staleFacts := openFacts()
	staleFacts.MyRoles = []string{"reviewer"}
	staleFacts.MyReviewState = "requested" // needs_review

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
		t.Errorf("fresh waiting_on_author should rank above stale needs_review; got %q first", items[0].NativeID)
	}
}

func TestAgeBandAbandonedLast(t *testing.T) {
	f := openFacts()
	f.MyRoles = []string{"reviewer"}
	f.MyReviewState = "requested" // needs_review for all

	freshSig := sig(2, "acme/api#fresh", signalMentioned, baseTime.Add(-1*time.Hour))
	staleSig := sig(4, "acme/api#stale", signalMentioned, baseTime.Add(-10*24*time.Hour))
	abandSig := sig(6, "acme/api#abandoned", signalMentioned, baseTime.Add(-40*24*time.Hour))

	events := []storage.StoredEvent{
		obs(1, "c", "acme/api#fresh", f), freshSig,
		obs(3, "c", "acme/api#stale", f), staleSig,
		obs(5, "c", "acme/api#abandoned", f), abandSig,
	}
	items := fold(events)
	if len(items) != 3 {
		t.Fatalf("want 3 items, got %d", len(items))
	}
	if items[0].NativeID != "acme/api#fresh" || items[1].NativeID != "acme/api#stale" || items[2].NativeID != "acme/api#abandoned" {
		t.Errorf("band order wrong: %q %q %q", items[0].NativeID, items[1].NativeID, items[2].NativeID)
	}
}

func TestAgeBandWithinBandOrderPreserved(t *testing.T) {
	// Two needs_review items, both fresh; newer should sort first (existing order).
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
	// A pinned abandoned item should stay pinned at the top (bands don't reorder pins).
	f := openFacts()
	f.MyRoles = []string{"reviewer"}
	f.MyReviewState = "requested"

	freshSig := sig(2, "acme/api#fresh", signalMentioned, baseTime.Add(-1*time.Hour))
	abandSig := sig(4, "acme/api#abandoned", signalMentioned, baseTime.Add(-40*24*time.Hour))

	ranked := Fold([]storage.StoredEvent{
		obs(1, "c", "acme/api#fresh", f), freshSig,
		obs(3, "c", "acme/api#abandoned", f), abandSig,
	}, baseTime, DefaultStaleThreshold, DefaultAbandonedThreshold)

	abandID := itemID(itemKey{"c", "merge_request", "acme/api#abandoned"})
	pinned := pin(ranked, []storage.PinnedItem{{ID: abandID}})

	if len(pinned) != 2 {
		t.Fatalf("want 2 items, got %d", len(pinned))
	}
	if pinned[0].ID != abandID || !pinned[0].Flagged {
		t.Errorf("abandoned pinned item should be first; got %q flagged=%v", pinned[0].ID, pinned[0].Flagged)
	}
}

// --- Pin age (FlaggedAt) tests ---

func TestPinFlaggedAtSurfaces(t *testing.T) {
	flagTime := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	items := []WorkItem{{ID: "a", States: []State{StateNeedsReview}}}
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
	// Three snapshots where needs_review is true throughout → onset = oldest.
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
	onset, ok := items[0].Since[string(StateNeedsReview)]
	if !ok {
		t.Fatal("needs_review should have onset in Since map")
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
	onset, ok := items[0].Since[string(StateNeedsReview)]
	if !ok {
		t.Fatal("needs_review should have onset in Since map")
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
