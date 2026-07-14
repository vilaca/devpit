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

// eventItemObserved is the event_type for a periodic item snapshot
// (docs/Event_Taxonomy_and_Storage.md).
const eventItemObserved = "item.observed"

// Normalized gate and detailed_merge_status values.
const (
	dmsMergeable      = "mergeable"
	dmsCIMustPass     = "ci_must_pass"
	dmsPolicyDenied   = "policies_denied"
	dmsSecurityPolicy = "security_policy_violations"
	gateReady         = "ready"
	gateBlocked       = "blocked"
	gateUnknown       = "unknown"
	stateOpen         = "open"
	stateMerged       = "merged"
	stateClosed       = "closed"
)

// mergeGate maps detailed_merge_status to the normalized gate class.
// Transient/draft statuses map to "unknown"; the fold carries the last known
// gate forward so transient reads don't flap buckets (docs/Event_Taxonomy_and_Storage.md).
func mergeGate(status string) string {
	switch status {
	case dmsMergeable:
		return gateReady
	case "unchecked", "checking", "preparing", "approvals_syncing", "ci_still_running", "draft_status", "":
		return gateUnknown
	default:
		// conflict, need_rebase, not_approved, requested_changes,
		// ci_must_pass, discussions_not_resolved, and the tier-specific gates.
		return gateBlocked
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
	state := stateOpen
	switch mr.State {
	case stateMerged:
		state = stateMerged
	case stateClosed, "locked":
		state = stateClosed
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

	// blocking_discussions_resolved is a raw "threads exist" fact, not a gate verdict —
	// it returns false even when the project allows merging with open threads.
	// Gate it on gateBlocked so the badge never appears on a ready item.
	gate := mergeGate(mr.DetailedMergeStatus)
	var unresolvedDiscussions bool
	if gate == gateBlocked {
		if mr.BlockingDiscussionsResolved != nil {
			unresolvedDiscussions = !*mr.BlockingDiscussionsResolved
		} else {
			unresolvedDiscussions = mr.DetailedMergeStatus == "discussions_not_resolved"
		}
	}

	payload := sdk.ItemObservedPayload{
		Title:         mr.Title,
		URL:           mr.WebURL,
		Repo:          repoFromRef(mr.References.Full),
		State:         state,
		Draft:         mr.Draft,
		Author:        mr.Author.Username,
		MyRoles:       roles,
		Gate:          gate,
		GateDetail:    mr.DetailedMergeStatus,
		FailingChecks: mr.DetailedMergeStatus == dmsCIMustPass, // GraphQL join refines via headPipeline.status
		MergeConflict: mr.HasConflicts,
		// GraphQL join ORs in shouldBeRebased + divergedFromTargetBranch.
		NeedsRebase:           mr.DetailedMergeStatus == "need_rebase",
		NeedsApproval:         mr.DetailedMergeStatus == "not_approved",
		UnresolvedDiscussions: unresolvedDiscussions,
		PolicyDenied:          isPolicyDenied(mr.DetailedMergeStatus),
		AutoMergeArmed:        mr.MergeWhenPipelineSucceeds, // REST list field; ChecksRunning is refined by the GraphQL join
		ProviderUpdatedAt:     mr.UpdatedAt,
		TicketKeys:            sdk.ExtractTicketKeys(mr.Title, mr.SourceBranch, mr.Description),
		Labels:                mr.Labels,
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

func isPolicyDenied(dms string) bool {
	return dms == dmsPolicyDenied || dms == dmsSecurityPolicy
}

func observedDedupeKey(p sdk.ItemObservedPayload) string {
	b, _ := json.Marshal(p) //nolint:errchkjson // payload has no unmarshalable fields; Marshal cannot fail here
	sum := sha256.Sum256(b)
	return "item.observed:" + hex.EncodeToString(sum[:])
}
