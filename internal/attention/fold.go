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

// WorkItem is one folded provider object: an open item that matches at least
// one attention state. Field names mirror docs/REST_API.md GET /attention; the
// API layer wraps this with connection label/type and the flagged pin.
type WorkItem struct {
	// ID is a short, URL-safe, stable handle derived from the identity triple
	// (connection_id, object_type, native_id) — see the "id" field notes in
	// docs/REST_API.md.
	ID            string         `json:"id"`
	ConnectionID  string         `json:"connection_id"`
	ObjectType    string         `json:"object_type"`
	NativeID      string         `json:"native_id"`
	Title         string         `json:"title"`
	URL           string         `json:"url"`
	Repo          string         `json:"repo"`
	Author        string         `json:"author"`
	Draft         bool           `json:"draft"`
	States        []State        `json:"states"`  // precedence order; States[0] ranks the item
	Flagged       bool           `json:"flagged"` // pinned in the "Handle next" zone
	Stale         bool           `json:"stale"`
	UpdatedAt     time.Time      `json:"updated_at"`
	SignalCounts  map[string]int `json:"signal_counts,omitempty"` // only types with count > 1
	FailingChecks bool           `json:"failing_checks"`
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
	now time.Time, staleThreshold time.Duration,
) ([]WorkItem, error) {
	var events []storage.StoredEvent
	for _, id := range connectionIDs {
		evs, err := db.ReadEvents(ctx, id, time.Time{})
		if err != nil {
			return nil, fmt.Errorf("read events for %q: %w", id, err)
		}
		events = append(events, evs...)
	}

	flagged, err := db.ListHandleNext(ctx)
	if err != nil {
		return nil, fmt.Errorf("list handle_next: %w", err)
	}

	return pin(Fold(events, now, staleThreshold), flagged), nil
}

// Fold folds an event log into the ranked WorkItem list. Events may span
// multiple connections; they are grouped by identity triple. now and
// staleThreshold drive the stale badge. The result is sorted by state
// precedence (highest first) then newest-first, with the item ID as a stable
// final tiebreak. Items that are removed, not open, or match no state are
// dropped — the list is actionable work only.
func Fold(events []storage.StoredEvent, now time.Time, staleThreshold time.Duration) []WorkItem {
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
		if item, ok := foldItem(k, groups[k], now, staleThreshold); ok {
			items = append(items, item)
		}
	}

	sortItems(items)
	return items
}

// foldItem folds one item's events into a WorkItem, or reports ok=false if the
// item should not appear (no snapshot, removed after the last snapshot, not
// open, malformed facts, or no matching state).
func foldItem(k itemKey, events []storage.StoredEvent, now time.Time, staleThreshold time.Duration) (WorkItem, bool) {
	var (
		latestObserved *storage.StoredEvent
		latestRemoved  int64
		signals        []storage.StoredEvent
		hasMention     bool
	)
	for i := range events {
		e := &events[i]
		switch {
		case e.EventType == eventItemObserved:
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

	states := statesFor(facts, hasMention)
	if len(states) == 0 {
		return WorkItem{}, false // open but not actionable — not in the list
	}

	updatedAt := rankingTime(signals, facts, latestObserved.ObservedAt)

	return WorkItem{
		ID:            itemID(k),
		ConnectionID:  k.connectionID,
		ObjectType:    k.objectType,
		NativeID:      k.nativeID,
		Title:         facts.Title,
		URL:           facts.URL,
		Repo:          facts.Repo,
		Author:        facts.Author,
		Draft:         facts.Draft,
		States:        states,
		Stale:         staleThreshold > 0 && now.Sub(updatedAt) > staleThreshold,
		UpdatedAt:     updatedAt,
		SignalCounts:  signalCounts(signals),
		FailingChecks: facts.FailingChecks,
	}, true
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

// sortItems orders items by state precedence (highest-precedence state first),
// then newest-first, then item ID for a stable total order.
func sortItems(items []WorkItem) {
	sort.SliceStable(items, func(i, j int) bool {
		ri, rj := rankOf[items[i].States[0]], rankOf[items[j].States[0]]
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
// (flaggedIDs is ordered by flagged_at ascending), each marked Flagged. The
// remaining items keep their auto-ranked order, and a pinned item appears only
// in the zone, never twice. Flagged IDs with no live item are ignored.
func pin(items []WorkItem, flaggedIDs []string) []WorkItem {
	if len(flaggedIDs) == 0 {
		return items
	}
	flagRank := make(map[string]int, len(flaggedIDs))
	for i, id := range flaggedIDs {
		flagRank[id] = i
	}

	pinned := make([]WorkItem, 0, len(flaggedIDs))
	rest := make([]WorkItem, 0, len(items))
	for _, it := range items {
		if _, ok := flagRank[it.ID]; ok {
			it.Flagged = true
			pinned = append(pinned, it)
		} else {
			rest = append(rest, it)
		}
	}
	sort.SliceStable(pinned, func(i, j int) bool {
		return flagRank[pinned[i].ID] < flagRank[pinned[j].ID]
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
