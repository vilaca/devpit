package gitlab

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

// mergeGate maps detailed_merge_status to the normalized gate class.
// Transient/draft statuses map to "unknown"; the fold carries the last known
// gate forward so transient reads don't flap buckets (taxonomy §2).
func mergeGate(status string) string {
	switch status {
	case "mergeable":
		return "ready"
	case "unchecked", "checking", "preparing", "approvals_syncing", "ci_still_running", "draft_status", "":
		return "unknown"
	default:
		// conflict, need_rebase, not_approved, requested_changes,
		// ci_must_pass, discussions_not_resolved, and the tier-specific gates.
		return "blocked"
	}
}

func nativeID(mr glMergeRequest) string {
	if mr.References.Full != "" {
		return mr.References.Full
	}
	return fmt.Sprintf("%d!%d", mr.ProjectID, mr.IID)
}

func repoFromRef(full string) string {
	for i := range len(full) {
		if full[i] == '!' {
			return full[:i]
		}
	}
	return full
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

func hasUser(users []glUser, handle string) bool {
	for _, u := range users {
		if u.Username == handle {
			return true
		}
	}
	return false
}

func (p *Provider) observedFromMR(mr glMergeRequest) sdk.Event {
	state := "open"
	switch mr.State {
	case "merged":
		state = "merged"
	case "closed", "locked":
		state = "closed"
	}

	roles := []string{}
	if mr.Author.Username == p.handle {
		roles = append(roles, "author")
	}
	if hasUser(mr.Reviewers, p.handle) {
		roles = append(roles, "reviewer")
	}
	if hasUser(mr.Assignees, p.handle) {
		roles = append(roles, "assignee")
	}
	sort.Strings(roles) // deterministic role order for the dedupe hash

	payload := sdk.ItemObservedPayload{
		Title:             mr.Title,
		URL:               mr.WebURL,
		Repo:              repoFromRef(mr.References.Full),
		State:             state,
		Draft:             mr.Draft,
		Author:            mr.Author.Username,
		MyRoles:           roles,
		Gate:              mergeGate(mr.DetailedMergeStatus),
		GateDetail:        mr.DetailedMergeStatus,
		ProviderUpdatedAt: mr.UpdatedAt,
	}

	return sdk.Event{
		ObjectType: objectType,
		NativeID:   nativeID(mr),
		EventType:  eventItemObserved,
		OccurredAt: parseTime(mr.UpdatedAt),
		Actor:      mr.Author.Username,
		DedupeKey:  observedDedupeKey(payload),
		Payload:    payload,
	}
}

func observedDedupeKey(p sdk.ItemObservedPayload) string {
	b, _ := json.Marshal(p) //nolint:errchkjson // payload is JSON-safe (scalar fields only); Marshal cannot fail here
	sum := sha256.Sum256(b)
	return "item.observed:" + hex.EncodeToString(sum[:])
}
