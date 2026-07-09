package attention

import "github.com/vilaca/devpit/sdk"

// State is one of the six v0.1 attention states (docs/Attention_Engine.md). A
// WorkItem may carry several at once; they render as tags.
type State string

// The six attention states. String values are the wire form used by the REST
// API (docs/REST_API.md GET /attention).
const (
	StateReadyToMerge     State = "ready_to_merge"
	StateNeedsReview      State = "needs_review"
	StateChangesRequested State = "changes_requested"
	StateBlocked          State = "blocked"
	StateMentioned        State = "mentioned"
	StateWaitingOnAuthor  State = "waiting_on_author"
)

// precedence lists the states highest-first. An item sorts by its
// highest-precedence state, and its States slice is emitted in this order.
// This is the canonical order — docs/Attention_Engine.md and docs/REST_API.md
// describe it but do not restate it. Action-demanding states rank above
// Ready to Merge (a quick win, but nothing is stuck there).
var precedence = []State{
	StateNeedsReview,
	StateChangesRequested,
	StateBlocked,
	StateReadyToMerge,
	StateMentioned,
	StateWaitingOnAuthor,
}

// rankOf maps a state to its precedence index (0 = highest).
var rankOf = func() map[State]int {
	m := make(map[State]int, len(precedence))
	for i, s := range precedence {
		m[s] = i
	}
	return m
}()

// Normalized fact values — see docs/Event_Taxonomy_and_Storage.md.
const (
	stateOpen                = "open"
	gateReady                = "ready"
	gateBlocked              = "blocked"
	decisionChangesRequested = "changes_requested"
	roleAuthor               = "author"
	roleReviewer             = "reviewer"

	// my_review_state values.
	reviewStateRequested        = "requested"
	reviewStateReviewed         = "reviewed"
	reviewStateApproved         = "approved"
	reviewStateChangesRequested = "changes_requested"
)

// statesFor evaluates the fold table (docs/Attention_Engine.md) against the
// latest facts and whether
// the item has any mention signal. Returns the matching states in precedence
// order (so States[0] is the highest-precedence state).
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

// matches reports whether a single state's condition holds. The Mentioned
// state is signal-driven; the other five derive from the latest facts.
func matches(s State, f sdk.ItemObservedPayload, roles map[string]bool, hasMention bool) bool {
	switch s {
	case StateReadyToMerge:
		return roles[roleAuthor] && !f.Draft && f.Gate == gateReady
	case StateNeedsReview:
		return roles[roleReviewer] && f.MyReviewState == reviewStateRequested
	case StateChangesRequested:
		return roles[roleAuthor] && f.ReviewDecision == decisionChangesRequested
	case StateBlocked:
		return roles[roleAuthor] && !f.Draft && f.Gate == gateBlocked
	case StateMentioned:
		return hasMention
	case StateWaitingOnAuthor:
		return roles[roleReviewer] && reviewIsDone(f.MyReviewState)
	default:
		return false
	}
}

// reviewIsDone reports whether the reviewer's own review has been submitted,
// putting the ball back with the author (the Waiting on Author state).
func reviewIsDone(myReviewState string) bool {
	switch myReviewState {
	case reviewStateReviewed, reviewStateApproved, reviewStateChangesRequested:
		return true
	default:
		return false
	}
}
