package attention

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/vilaca/devpit/internal/storage"
	"github.com/vilaca/devpit/sdk"
)

// Event types folded here — see docs/Event_Taxonomy_and_Storage.md.
const (
	eventItemObserved = "item.observed"
	eventItemRemoved  = "item.removed"
	signalPrefix      = "signal."
	signalMentioned   = "signal.mentioned"
)

// DefaultStaleThreshold is the age past which an item earns the "stale" badge
// — the anti-rot safety net (docs/Attention_Engine.md). Callers may override
// via Fold's staleThreshold argument. A non-positive threshold disables the
// badge.
const DefaultStaleThreshold = 7 * 24 * time.Hour

// DefaultOldThreshold is the age past which an item earns the "old"
// badge (idle > 30 days). Mutually exclusive with stale. A non-positive value
// disables the tier; items older than 30 days are then simply stale.
const DefaultOldThreshold = 30 * 24 * time.Hour

// WorkItem is one folded provider object: an open item that matches at least
// one attention state. Field names mirror docs/REST_API.md GET /attention; the
// API layer wraps this with connection label/type and the flagged pin.
type WorkItem struct {
	// ID is a short, URL-safe, stable handle derived from the identity triple
	// (connection_id, object_type, native_id) — see the "id" field notes in
	// docs/REST_API.md.
	ID                    string               `json:"id"`
	ConnectionID          string               `json:"connection_id"`
	ObjectType            string               `json:"object_type"`
	NativeID              string               `json:"native_id"`
	Title                 string               `json:"title"`
	URL                   string               `json:"url"`
	Repo                  string               `json:"repo"`
	Author                string               `json:"author"`
	Draft                 bool                 `json:"draft"`
	States                []State              `json:"states"`          // precedence order; States[0] ranks the item
	Muted                 bool                 `json:"muted,omitempty"` // reviewed-done: demoted + de-emphasized
	Flagged               bool                 `json:"flagged"`         // pinned in the "Handle next" zone
	Stale                 bool                 `json:"stale"`
	Old                   bool                 `json:"old"`
	UpdatedAt             time.Time            `json:"updated_at"`
	SignalCounts          map[string]int       `json:"signal_counts,omitempty"` // only types with count > 1
	AutoMergeArmed        bool                 `json:"auto_merge_armed"`
	ChecksRunning         bool                 `json:"checks_running"`
	FailingChecks         bool                 `json:"failing_checks"`
	MergeConflict         bool                 `json:"merge_conflict"`
	NeedsRebase           bool                 `json:"needs_rebase"`
	NeedsApproval         bool                 `json:"needs_approval"`
	UnresolvedDiscussions bool                 `json:"unresolved_discussions"`
	PolicyDenied          bool                 `json:"policy_denied"`
	ApprovalsCount        int                  `json:"approvals_count,omitempty"`
	MyReviewState         string               `json:"my_review_state,omitempty"` // approved|changes_requested|reviewed|""
	GateDetail            string               `json:"gate_detail,omitempty"`
	FlaggedAt             *time.Time           `json:"flagged_at,omitempty"`
	Since                 map[string]time.Time `json:"since,omitempty"` // onset of each active tag
	TicketKeys            []string             `json:"ticket_keys,omitempty"`
}

// itemKey is the identity triple that groups events into one WorkItem.
type itemKey struct {
	connectionID string
	objectType   string
	nativeID     string
}

// List is the read model behind GET /attention. It reads the full event log
// for the given connections plus the "Handle next" pins, folds the events, and
// returns the ranked list with pinned items first. The API layer adds
// per-connection label/type from config, which lives outside the event log.
func List(
	ctx context.Context, db *storage.DB, connectionIDs []string,
	now time.Time, staleThreshold, oldThreshold time.Duration,
) ([]WorkItem, error) {
	var events []storage.StoredEvent
	for _, id := range connectionIDs {
		evs, err := db.ReadEvents(ctx, id, time.Time{})
		if err != nil {
			return nil, fmt.Errorf("read events for %q: %w", id, err)
		}
		events = append(events, evs...)
	}

	pinned, err := db.ListHandleNext(ctx)
	if err != nil {
		return nil, fmt.Errorf("list handle_next: %w", err)
	}

	return pin(Fold(events, now, staleThreshold, oldThreshold), pinned), nil
}

// Fold folds an event log into the ranked WorkItem list. Events may span
// multiple connections; they are grouped by identity triple. now,
// staleThreshold, and oldThreshold drive the age tiers. The result is
// sorted by age band (fresh, stale, old) then state precedence
// (highest first) then newest-first, with the item ID as a stable final
// tiebreak. Stateless items sort below every actionable state. Items that are
// removed or not open (merged/closed) are dropped.
func Fold(
	events []storage.StoredEvent, now time.Time,
	staleThreshold, oldThreshold time.Duration,
) []WorkItem {
	groups := make(map[itemKey][]storage.StoredEvent)
	order := make([]itemKey, 0)
	for _, e := range events {
		k := itemKey{e.ConnectionID, e.ObjectType, e.NativeID}
		if _, seen := groups[k]; !seen {
			order = append(order, k)
		}
		groups[k] = append(groups[k], e)
	}

	items := make([]WorkItem, 0, len(order))
	for _, k := range order {
		if item, ok := foldItem(k, groups[k], now, staleThreshold, oldThreshold); ok {
			items = append(items, item)
		}
	}

	sortItems(items)
	return items
}

// foldItem folds one item's events into a WorkItem, or reports ok=false if the
// item should not appear (no snapshot, removed after the last snapshot, not
// open, or malformed facts). An open item with no matching state still appears
// — only merged/closed and removed items vanish.
func foldItem(
	k itemKey, events []storage.StoredEvent, now time.Time,
	staleThreshold, oldThreshold time.Duration,
) (WorkItem, bool) {
	var (
		latestObserved *storage.StoredEvent
		allObserved    []storage.StoredEvent
		latestRemoved  int64
		signals        []storage.StoredEvent
		mentionSigs    []storage.StoredEvent
		hasMention     bool
	)
	for i := range events {
		e := &events[i]
		switch {
		case e.EventType == eventItemObserved:
			allObserved = append(allObserved, *e)
			if latestObserved == nil || e.ID > latestObserved.ID {
				latestObserved = e
			}
		case e.EventType == eventItemRemoved:
			if e.ID > latestRemoved {
				latestRemoved = e.ID
			}
		case strings.HasPrefix(e.EventType, signalPrefix):
			signals = append(signals, *e)
			if e.EventType == signalMentioned {
				hasMention = true
				mentionSigs = append(mentionSigs, *e)
			}
		}
	}

	// No facts to show, or the item was removed after its last snapshot
	// (reappearance resumes via a newer snapshot, so compare by insertion order).
	if latestObserved == nil || latestRemoved > latestObserved.ID {
		return WorkItem{}, false
	}

	var facts sdk.ItemObservedPayload
	if err := json.Unmarshal(latestObserved.Payload, &facts); err != nil {
		return WorkItem{}, false // malformed snapshot — skip rather than fail the whole fold
	}
	if facts.State != stateOpen {
		return WorkItem{}, false // merged/closed vanish
	}

	// Every open item we hold is one the user is involved in (the reconcile
	// scopes are assigned_to_me/created_by_me, plus mention signals), so an open
	// item always shows. States are still computed to drive tags and ranking,
	// but an empty set no longer hides the item — it renders as a plain row
	// (authored ones carry the blue "mine" tint; drafts carry the Draft tag).
	states := statesFor(facts, hasMention)
	if states == nil {
		states = []State{} // keep the JSON "states" field an array, never null
	}

	updatedAt := rankingTime(signals, facts, latestObserved.ObservedAt)

	idle := now.Sub(updatedAt)
	old := oldThreshold > 0 && idle > oldThreshold
	stale := !old && staleThreshold > 0 && idle > staleThreshold

	// Reviewed-done: I'm a reviewer (not the author) who has submitted a review,
	// so there is nothing left for me to do. Such items are muted and demoted
	// below everything else, even a stale mention (ADR-0016).
	roles := rolesSet(facts.MyRoles)
	muted := roles[roleReviewer] && !roles[roleAuthor] && reviewIsDone(facts.MyReviewState)

	// Sort observed events newest-first for onset computation.
	sort.Slice(allObserved, func(i, j int) bool { return allObserved[i].ID > allObserved[j].ID })

	return WorkItem{
		ID:                    itemID(k),
		ConnectionID:          k.connectionID,
		ObjectType:            k.objectType,
		NativeID:              k.nativeID,
		Title:                 facts.Title,
		URL:                   facts.URL,
		Repo:                  facts.Repo,
		Author:                facts.Author,
		Draft:                 facts.Draft,
		States:                states,
		Muted:                 muted,
		Stale:                 stale,
		Old:                   old,
		UpdatedAt:             updatedAt,
		SignalCounts:          signalCounts(signals),
		AutoMergeArmed:        facts.AutoMergeArmed,
		ChecksRunning:         facts.ChecksRunning,
		FailingChecks:         facts.FailingChecks,
		MergeConflict:         facts.MergeConflict,
		NeedsRebase:           facts.NeedsRebase,
		NeedsApproval:         facts.NeedsApproval,
		UnresolvedDiscussions: facts.UnresolvedDiscussions,
		PolicyDenied:          facts.PolicyDenied,
		ApprovalsCount:        facts.ApprovalsCount,
		MyReviewState:         facts.MyReviewState,
		GateDetail:            facts.GateDetail,
		Since:                 computeSince(allObserved, states, facts, hasMention, mentionSigs),
		TicketKeys:            facts.TicketKeys,
	}, true
}

// computeSince returns the Since map: onset timestamp for each currently-active
// tag (state or marker). Only tags that are active appear in the map.
//
// For state tags (except mentioned): onset = start of the latest contiguous run
// of snapshots where the condition holds, walking newest → oldest.
// For mentioned: onset = earliest mention signal's coalesce(occurred_at, observed_at).
// For marker tags (draft, failing_checks, merge_conflict, needs_rebase): same
// contiguous-run logic as states.
//
// Accuracy is bounded by observation history and poll cadence (documented gap).
func computeSince(
	allObserved []storage.StoredEvent, // sorted newest-first
	activeStates []State,
	facts sdk.ItemObservedPayload,
	hasMention bool,
	mentionSigs []storage.StoredEvent,
) map[string]time.Time {
	if len(allObserved) == 0 && !hasMention {
		return nil
	}

	since := make(map[string]time.Time)

	// State onsets (except mentioned — signal-driven).
	for _, s := range activeStates {
		if s == StateMentioned {
			continue
		}
		onset := onsetForCondition(allObserved, func(p sdk.ItemObservedPayload) bool {
			roles := rolesSet(p.MyRoles)
			return matches(s, p, roles, false)
		})
		if !onset.IsZero() {
			since[string(s)] = onset
		}
	}

	// Mentioned onset: earliest mention signal.
	if hasMention {
		var earliest time.Time
		for _, sig := range mentionSigs {
			t := sig.ObservedAt
			if sig.OccurredAt != nil {
				t = *sig.OccurredAt
			}
			if earliest.IsZero() || t.Before(earliest) {
				earliest = t
			}
		}
		if !earliest.IsZero() {
			since[string(StateMentioned)] = earliest
		}
	}

	// Marker onsets.
	type markerCheck struct {
		key    string
		active bool
		check  func(sdk.ItemObservedPayload) bool
	}
	for _, mc := range []markerCheck{
		{"draft", facts.Draft, func(p sdk.ItemObservedPayload) bool { return p.Draft }},
		{"failing_checks", facts.FailingChecks, func(p sdk.ItemObservedPayload) bool { return p.FailingChecks }},
		{"merge_conflict", facts.MergeConflict, func(p sdk.ItemObservedPayload) bool { return p.MergeConflict }},
		{"needs_rebase", facts.NeedsRebase, func(p sdk.ItemObservedPayload) bool { return p.NeedsRebase }},
		{"needs_approval", facts.NeedsApproval, func(p sdk.ItemObservedPayload) bool { return p.NeedsApproval }},
		{"unresolved_discussions", facts.UnresolvedDiscussions,
			func(p sdk.ItemObservedPayload) bool { return p.UnresolvedDiscussions }},
		{"policy_denied", facts.PolicyDenied, func(p sdk.ItemObservedPayload) bool { return p.PolicyDenied }},
	} {
		if !mc.active {
			continue
		}
		if onset := onsetForCondition(allObserved, mc.check); !onset.IsZero() {
			since[mc.key] = onset
		}
	}

	if len(since) == 0 {
		return nil
	}
	return since
}

// onsetForCondition walks allObserved (newest-first) and returns the time of
// the oldest event in the current contiguous run where check returns true.
// Returns zero if the condition isn't active in the first (latest) snapshot.
func onsetForCondition(allObserved []storage.StoredEvent, check func(sdk.ItemObservedPayload) bool) time.Time {
	var onset time.Time
	for _, ev := range allObserved {
		var p sdk.ItemObservedPayload
		if err := json.Unmarshal(ev.Payload, &p); err != nil {
			break // unreadable snapshot — stop extending run
		}
		if !check(p) {
			break // run ended
		}
		onset = snapshotTime(p.ProviderUpdatedAt, ev.ObservedAt)
	}
	return onset
}

// snapshotTime returns the provider_updated_at time when parseable, else observed_at.
func snapshotTime(providerUpdatedAt string, observedAt time.Time) time.Time {
	if providerUpdatedAt != "" {
		if t, err := time.Parse(time.RFC3339, providerUpdatedAt); err == nil {
			return t
		}
	}
	return observedAt
}

// rolesSet converts a roles slice to a lookup map for matches().
func rolesSet(roles []string) map[string]bool {
	m := make(map[string]bool, len(roles))
	for _, r := range roles {
		m[r] = true
	}
	return m
}

// rankingTime is the item's ranking timestamp: the newest
// coalesce(occurred_at, observed_at) across its signal events, else the latest
// snapshot's provider_updated_at, falling back to that snapshot's observed_at
// when the provider time is absent or unparseable.
func rankingTime(signals []storage.StoredEvent, facts sdk.ItemObservedPayload, snapshotObservedAt time.Time) time.Time {
	var newest time.Time
	for _, s := range signals {
		t := s.ObservedAt
		if s.OccurredAt != nil {
			t = *s.OccurredAt
		}
		if t.After(newest) {
			newest = t
		}
	}
	if !newest.IsZero() {
		return newest
	}
	if facts.ProviderUpdatedAt != "" {
		if t, err := time.Parse(time.RFC3339, facts.ProviderUpdatedAt); err == nil {
			return t
		}
	}
	return snapshotObservedAt
}

// signalCounts tallies signals per type (prefix stripped, e.g. "mentioned"),
// keeping only types that occur more than once — these drive the "×N" tags.
// Returns nil when nothing repeats, so the JSON field is omitted.
func signalCounts(signals []storage.StoredEvent) map[string]int {
	counts := make(map[string]int, len(signals))
	for _, s := range signals {
		counts[strings.TrimPrefix(s.EventType, signalPrefix)]++
	}
	var repeated map[string]int
	for typ, n := range counts {
		if n > 1 {
			if repeated == nil {
				repeated = make(map[string]int)
			}
			repeated[typ] = n
		}
	}
	return repeated
}

// stateRank is the precedence rank of an item's ranking state, or one past the
// lowest state for a stateless item so those sort below every actionable one.
func stateRank(it WorkItem) int {
	if len(it.States) == 0 {
		return len(precedence)
	}
	return rankOf[it.States[0]]
}

// ageBand returns 0 for fresh, 1 for stale, 2 for old.
func ageBand(it WorkItem) int {
	if it.Old {
		return 2
	}
	if it.Stale {
		return 1
	}
	return 0
}

// sortItems orders items by reviewed-done first (muted items sink to the very
// bottom, beneath even old ones), then age band (fresh < stale < old), then
// state precedence (highest-precedence state), then newest-first, then item ID
// for a stable total order. Pinned items are not sorted here — pin() handles them.
func sortItems(items []WorkItem) {
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Muted != items[j].Muted {
			return !items[i].Muted // non-muted first
		}
		bi, bj := ageBand(items[i]), ageBand(items[j])
		if bi != bj {
			return bi < bj
		}
		ri, rj := stateRank(items[i]), stateRank(items[j])
		if ri != rj {
			return ri < rj
		}
		if !items[i].UpdatedAt.Equal(items[j].UpdatedAt) {
			return items[i].UpdatedAt.After(items[j].UpdatedAt)
		}
		return items[i].ID < items[j].ID
	})
}

// pin lifts flagged items into the pinned zone: they come first, in flag order
// (pinnedItems is ordered by flagged_at ascending), each marked Flagged with
// its FlaggedAt timestamp. The remaining items keep their auto-ranked order,
// and a pinned item appears only in the zone, never twice. Flagged IDs with no
// live item are ignored.
func pin(items []WorkItem, pinnedItems []storage.PinnedItem) []WorkItem {
	if len(pinnedItems) == 0 {
		return items
	}
	type pinMeta struct {
		rank      int
		flaggedAt time.Time
	}
	flagMeta := make(map[string]pinMeta, len(pinnedItems))
	for i, p := range pinnedItems {
		flagMeta[p.ID] = pinMeta{rank: i, flaggedAt: p.FlaggedAt}
	}

	pinned := make([]WorkItem, 0, len(pinnedItems))
	rest := make([]WorkItem, 0, len(items))
	for _, it := range items {
		if meta, ok := flagMeta[it.ID]; ok {
			it.Flagged = true
			if !meta.flaggedAt.IsZero() {
				t := meta.flaggedAt
				it.FlaggedAt = &t
			}
			pinned = append(pinned, it)
		} else {
			rest = append(rest, it)
		}
	}
	sort.SliceStable(pinned, func(i, j int) bool {
		return flagMeta[pinned[i].ID].rank < flagMeta[pinned[j].ID].rank
	})
	return append(pinned, rest...)
}

// itemID derives the stable public handle from the identity triple. It is the
// first 8 bytes of the SHA-256 of the NUL-joined triple, hex-encoded (16 chars),
// matching the REST example. NUL separators keep the mapping injective.
func itemID(k itemKey) string {
	sum := sha256.Sum256([]byte(k.connectionID + "\x00" + k.objectType + "\x00" + k.nativeID))
	return hex.EncodeToString(sum[:8])
}
