package github

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/vilaca/devpit/sdk"
)

const objectType = "merge_request"

// eventItemObserved is the event_type for a periodic item snapshot (taxonomy §1).
const eventItemObserved = "item.observed"

// mergeGate maps GitHub's mergeable_state to the normalized gate class.
// Transient values ("unknown", "") map to "unknown" and per the taxonomy
// never overwrite a known gate downstream (the synthesizer carries forward);
// the provider simply reports what it saw.
func mergeGate(mergeableState string) string {
	switch mergeableState {
	case "clean", "has_hooks":
		return "ready"
	case "blocked", "dirty", "behind":
		return "blocked"
	case "unstable":
		// non-gating check failure: mergeable, not Blocked (§4).
		return "ready"
	default: // "unknown", "draft", ""
		return "unknown"
	}
}

func nativeID(repo string, number int) string {
	return fmt.Sprintf("%s#%d", repo, number)
}

func parseTime(s string) *time.Time {
	if s == "" {
		return nil
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return nil
	}
	return &t
}

func hasHandle(users []ghUser, handle string) bool {
	for _, u := range users {
		if u.Login == handle {
			return true
		}
	}
	return false
}

// observedFromPull builds an item.observed event from a full PR detail.
func (p *Provider) observedFromPull(pr ghPull) sdk.Event {
	repo := pr.Base.Repo.FullName
	state := "open"
	if pr.Merged {
		state = "merged"
	} else if pr.State == "closed" {
		state = "closed"
	}

	roles := []string{}
	if pr.User.Login == p.handle {
		roles = append(roles, "author")
	}
	if hasHandle(pr.RequestedReviewers, p.handle) {
		roles = append(roles, "reviewer")
	}
	if hasHandle(pr.Assignees, p.handle) {
		roles = append(roles, "assignee")
	}

	gate := mergeGate(pr.MergeableState)
	payload := sdk.ItemObservedPayload{
		Title:             pr.Title,
		URL:               pr.HTMLURL,
		Repo:              repo,
		State:             state,
		Draft:             pr.Draft,
		Author:            pr.User.Login,
		MyRoles:           roles,
		Gate:              gate,
		GateDetail:        pr.MergeableState,
		FailingChecks:     pr.MergeableState == "unstable" || pr.MergeableState == "dirty",
		ProviderUpdatedAt: pr.UpdatedAt,
	}

	nid := nativeID(repo, pr.Number)
	return sdk.Event{
		ObjectType: objectType,
		NativeID:   nid,
		EventType:  eventItemObserved,
		OccurredAt: parseTime(pr.UpdatedAt),
		Actor:      pr.User.Login,
		DedupeKey:  observedDedupeKey(payload),
		Payload:    payload,
	}
}

// observedFromSearch builds an item.observed from a search row (no merge-gate
// data — the REST search result omits mergeable_state, so gate is unknown and
// the fold keeps the last known value).
func (p *Provider) observedFromSearch(it ghSearchItem, repo string, roles []string) sdk.Event {
	payload := sdk.ItemObservedPayload{
		Title:             it.Title,
		URL:               it.HTMLURL,
		Repo:              repo,
		State:             "open",
		Draft:             it.Draft,
		Author:            it.User.Login,
		MyRoles:           roles,
		Gate:              "unknown",
		ProviderUpdatedAt: it.UpdatedAt,
	}
	return sdk.Event{
		ObjectType: objectType,
		NativeID:   nativeID(repo, it.Number),
		EventType:  eventItemObserved,
		OccurredAt: parseTime(it.UpdatedAt),
		Actor:      it.User.Login,
		DedupeKey:  observedDedupeKey(payload),
		Payload:    payload,
	}
}

// observedDedupeKey is a hash of the canonical fact set (taxonomy §5): the
// same facts hash identically so re-polls dedupe, a changed fact makes a new
// snapshot.
func observedDedupeKey(p sdk.ItemObservedPayload) string {
	b, _ := json.Marshal(p) //nolint:errchkjson // payload is JSON-safe (scalar fields only); Marshal cannot fail here
	sum := sha256.Sum256(b)
	return "item.observed:" + hex.EncodeToString(sum[:])
}

func sortedRoles(roles []string) []string {
	sort.Strings(roles)
	return roles
}
