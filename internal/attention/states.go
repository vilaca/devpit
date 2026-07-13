package attention

import "github.com/vilaca/devpit/sdk"

// State is a provider signal (ADR/ADR-0021_Signal_Based_Presentation.md, folded
// into ADR/ADR-0016_Presentation_And_Ranking.md). A WorkItem may carry several
// at once; they render as chips in precedence order.
type State string

// Nine signals in wire form (REST API docs/REST_API.md GET /attention).
// Precedence order: highest first. Author-scoped gate/verdict signals keep
// their roles[author] guard; reviewer signals keep roles[reviewer]; mentioned is
// any-role; checking is role-neutral (the bare-row backstop).
const (
	StateChangesRequested State = "changes_requested"
	StateReviewRequested  State = "review_requested" // was StateNeedsReview / "needs_review"
	StateBlocked          State = "blocked"
	StateMentioned        State = "mentioned"
	StateReadyToMerge     State = "ready_to_merge"
	StateAutoMergeArmed   State = "auto_merge_armed"
	StateChecksRunning    State = "checks_running"
	StateChecking         State = "checking"
	StateReviewSubmitted  State = "review_submitted" // was StateWaitingOnAuthor / "waiting_on_author"
)

// precedence lists signals highest-first. It drives the order signals appear
// within an item's States slice (States[0] is the highest-precedence signal and
// renders as the leading chip). It no longer drives ranking — items rank by age
// band then recency (ADR-0016).
var precedence = []State{
	StateChangesRequested,
	StateReviewRequested,
	StateBlocked,
	StateMentioned,
	StateReadyToMerge,
	StateAutoMergeArmed,
	StateChecksRunning,
	StateChecking,
	StateReviewSubmitted,
}

// Normalized fact values — see docs/Event_Taxonomy_and_Storage.md.
const (
	stateOpen                = "open"
	gateReady                = "ready"
	gateBlocked              = "blocked"
	gateUnknown              = "unknown"
	decisionChangesRequested = "changes_requested"
	roleAuthor               = "author"
	roleReviewer             = "reviewer"

	// my_review_state values.
	reviewStateRequested        = "requested"
	reviewStateReviewed         = "reviewed"
	reviewStateApproved         = "approved"
	reviewStateChangesRequested = "changes_requested"
)

// statesFor evaluates the signal table against the latest facts and whether
// the item has any mention signal. Returns matching signals in precedence order
// (States[0] is the highest-precedence signal).
func statesFor(f sdk.ItemObservedPayload, hasMention bool) []State {
	roles := make(map[string]bool, len(f.MyRoles))
	for _, r := range f.MyRoles {
		roles[r] = true
	}

	var states []State
	for _, s := range precedence {
		if matches(s, f, roles, hasMention) {
			states = append(states, s)
		}
	}
	return states
}

// matches reports whether a single signal's condition holds.
func matches(s State, f sdk.ItemObservedPayload, roles map[string]bool, hasMention bool) bool {
	switch s {
	case StateChangesRequested:
		return roles[roleAuthor] && f.ReviewDecision == decisionChangesRequested
	case StateReviewRequested:
		return roles[roleReviewer] && f.MyReviewState == reviewStateRequested
	case StateBlocked:
		return roles[roleAuthor] && !f.Draft && f.Gate == gateBlocked
	case StateMentioned:
		return hasMention
	case StateReadyToMerge:
		return roles[roleAuthor] && !f.Draft && f.Gate == gateReady
	case StateAutoMergeArmed:
		return roles[roleAuthor] && !f.Draft && f.AutoMergeArmed
	case StateChecksRunning:
		return roles[roleAuthor] && !f.Draft && f.ChecksRunning
	case StateChecking:
		return f.Gate == gateUnknown // role-neutral backstop (D3)
	case StateReviewSubmitted:
		return roles[roleReviewer] && reviewIsDone(f.MyReviewState)
	default:
		return false
	}
}

// reviewIsDone reports whether the reviewer's own review has been submitted.
func reviewIsDone(myReviewState string) bool {
	switch myReviewState {
	case reviewStateReviewed, reviewStateApproved, reviewStateChangesRequested:
		return true
	default:
		return false
	}
}
