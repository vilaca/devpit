package gitlab

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/vilaca/devpit/sdk"
)

const mrQueryFmt = `a%d:project(fullPath:"%s"){mergeRequest(iid:"%d"){` +
	`approved shouldBeRebased divergedFromTargetBranch conflicts ` +
	`headPipeline{status} approvedBy{count nodes{username}} ` +
	`reviewers{nodes{username mergeRequestInteraction{reviewState}}}}}`

// graphQLBatchSize is the max MRs per GraphQL query. Each MR node costs ≈24
// complexity in practice — more than the field count suggests, because the
// approvedBy and reviewers connections are each scored — and GitLab's ceiling is
// 250. An earlier estimate of ≈18/node put the batch at 12 (× 23 = 276), which
// overshot the ceiling on real instances. 8 × 24 = 192 stays under with headroom
// for stricter instances.
const graphQLBatchSize = 8

// Normalized my_review_state values recorded for the authenticated user's own
// review verdict (see internal/attention: reviewIsDone treats all three as a
// submitted review). "approved" is also recorded when the user appears in a
// merge request's approvedBy set.
const (
	reviewStateApproved         = "approved"
	reviewStateReviewed         = "reviewed"
	reviewStateChangesRequested = "changes_requested"
)

// GitLab reviewer reviewState enum (glReviewState*) and the normalized
// review_decision the REQUESTED_CHANGES verdict produces for the author's
// changes-requested signal.
const (
	glReviewStateApproved         = "APPROVED"
	glReviewStateReviewed         = "REVIEWED"
	glReviewStateChangesRequested = "REQUESTED_CHANGES"
	decisionChangesRequested      = "changes_requested"
)

// graphQLError is returned by doGraphQL when the server responds HTTP 200 but
// includes a non-empty errors array with null data (e.g. complexity-ceiling rejection).
type graphQLError struct {
	msg string
}

func (e *graphQLError) Error() string { return "gitlab graphql: " + e.msg }

// doGraphQL POSTs a GraphQL query to the GitLab GraphQL API and returns the "data" map.
// Returns *graphQLError when the server returns HTTP 200 with a non-empty errors field
// and null data — this is how GitLab signals a complexity-ceiling rejection.
func (p *Provider) doGraphQL(ctx context.Context, query string) (map[string]json.RawMessage, error) {
	body, _ := json.Marshal(struct { //nolint:errchkjson // struct has no interface fields; Marshal cannot fail
		Query string `json:"query"`
	}{query})

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.graphqlEndpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+p.cfg.Token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", userAgent)

	resp, err := p.http.Do(req)
	if err != nil {
		return nil, err
	}

	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		// proceed
	case resp.StatusCode == http.StatusUnauthorized:
		_ = resp.Body.Close()
		return nil, sdk.ErrUnauthorized
	case resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests:
		d := parseRateDelay(resp)
		_ = resp.Body.Close()
		return nil, &sdk.RateLimitError{RetryAfter: d}
	default:
		_ = resp.Body.Close()
		return nil, &sdk.StatusError{Status: resp.StatusCode}
	}

	var result struct {
		Data   map[string]json.RawMessage `json:"data"`
		Errors json.RawMessage            `json:"errors"`
	}
	if err := decodeJSON(resp, &result); err != nil {
		return nil, err
	}
	// GitLab returns HTTP 200 with data:null and a non-empty errors array when the
	// query exceeds the complexity ceiling. Surface this as an error so callers can
	// record a degraded outcome instead of silently treating all nodes as missing.
	if result.Data == nil && len(result.Errors) > 0 {
		var errs []struct {
			Message string `json:"message"`
		}
		msg := "server returned errors with no data"
		if json.Unmarshal(result.Errors, &errs) == nil && len(errs) > 0 {
			msg = errs[0].Message
		}
		return nil, &graphQLError{msg: msg}
	}
	return result.Data, nil
}

type glPipeline struct {
	Status string `json:"status"`
}

// glGraphQLMR holds the GraphQL join result for one GitLab MR.
type glGraphQLMR struct {
	Approved     bool        `json:"approved"`
	ShouldRebase bool        `json:"shouldBeRebased"`
	Diverged     bool        `json:"divergedFromTargetBranch"`
	Conflicts    bool        `json:"conflicts"`
	HeadPipeline *glPipeline `json:"headPipeline"`
	ApprovedBy   struct {
		Count int `json:"count"`
		Nodes []struct {
			Username string `json:"username"`
		} `json:"nodes"`
	} `json:"approvedBy"`
	Reviewers struct {
		Nodes []struct {
			Username    string `json:"username"`
			Interaction struct {
				ReviewState string `json:"reviewState"`
			} `json:"mergeRequestInteraction"`
		} `json:"nodes"`
	} `json:"reviewers"`
}

// graphqlJoin enriches item.observed events with GitLab GraphQL data.
// Returns the enriched events and a degraded flag (true when at least one batch
// failed). On failure it logs and falls back to last-known enrichment from
// openSnapshots (B3: fail closed), so good data is never downgraded to nil.
// Invariant: it never drops or reorders the input events — every event appears in
// the output, enriched or carried forward. The engine derives the reconcile swept
// set from the result's events (ADR-0024) and relies on this; preserve it.
// Draft suppression: NeedsApproval, NeedsRebase, FailingChecks, ChecksRunning,
// ApprovalsCount, and MyReviewState are zeroed for draft MRs. MergeConflict is
// not draft-suppressed (mirrors REST has_conflicts behavior).
// glBatchItem identifies one MR to enrich via GraphQL: evIdx is its index in the
// caller's events slice; fullPath/iid locate it; draft gates suppression.
type glBatchItem struct {
	evIdx    int
	fullPath string
	iid      int
	draft    bool
}

// mrBatchQuery locates one MR for the GraphQL join by its GraphQL path + iid.
type mrBatchQuery struct {
	fullPath string
	iid      int
}

// runMRBatches queries mrQueryFmt for the given MRs in batches kept under
// GitLab's complexity ceiling and returns each resolved MR node keyed by its
// index in queries, plus a degraded flag set when any batch's doGraphQL call
// failed. Missing/null nodes are skipped (left for the caller's fallback). This
// is the single owner of the batch-build + doGraphQL + null-node-guard loop
// shared by the reconcile join (runGraphQLBatches) and the FastPoll open-set
// refresh (openSetRefresh) — an in-package duplication, not the cross-provider
// kind ADR-0003 forbids, so consolidating it keeps the null-node guard and query
// construction in one place (A8).
func (p *Provider) runMRBatches(ctx context.Context, queries []mrBatchQuery) (map[int]glGraphQLMR, bool) {
	results := make(map[int]glGraphQLMR, len(queries))
	var degraded bool
	for start := 0; start < len(queries); start += graphQLBatchSize {
		batch := queries[start:min(start+graphQLBatchSize, len(queries))]

		var q strings.Builder
		q.WriteString("query{")
		for j, it := range batch {
			fmt.Fprintf(&q, mrQueryFmt, j, it.fullPath, it.iid)
		}
		q.WriteString("}")

		data, err := p.doGraphQL(ctx, q.String())
		if err != nil {
			log.Printf("devpit: gitlab graphql batch degraded: %v", err)
			degraded = true
			continue
		}

		for j := range batch {
			raw, ok := data[fmt.Sprintf("a%d", j)]
			if !ok || raw == nil {
				continue
			}
			var node struct {
				MergeRequest *glGraphQLMR `json:"mergeRequest"`
			}
			if json.Unmarshal(raw, &node) == nil && node.MergeRequest != nil {
				results[start+j] = *node.MergeRequest
			}
		}
	}
	return results, degraded
}

// runGraphQLBatches queries the join fields for items and returns the per-evIdx
// results plus a degraded flag set when any batch failed.
func (p *Provider) runGraphQLBatches(ctx context.Context, items []glBatchItem) (map[int]glGraphQLMR, bool) {
	queries := make([]mrBatchQuery, len(items))
	for i, it := range items {
		queries[i] = mrBatchQuery{it.fullPath, it.iid}
	}
	byIndex, degraded := p.runMRBatches(ctx, queries)
	results := make(map[int]glGraphQLMR, len(byIndex))
	for i, mr := range byIndex {
		results[items[i].evIdx] = mr
	}
	return results, degraded
}

// glNote is one element returned by the MR notes REST endpoint.
// Only system notes are used; non-system notes are skipped by the matcher.
type glNote struct {
	ID     int    `json:"id"`
	System bool   `json:"system"`
	Body   string `json:"body"`
	Author struct {
		Username string `json:"username"`
	} `json:"author"`
	CreatedAt string `json:"created_at"`
}

// Exact system-note body strings for verdict events (verified 2026-07-17 against
// gitlab.relexsolutions.com /api/v4/projects/:id/merge_requests/:iid/notes).
const (
	glNoteBodyApproved         = "approved this merge request"
	glNoteBodyChangesRequested = "requested changes"
)

// fetchVerdictNotes fetches page 1 of MR notes (newest-first) from the REST API.
// Returns nil on any HTTP or parse error so the caller can retry next cycle.
func (p *Provider) fetchVerdictNotes(ctx context.Context, fullPath string, iid int) []glNote {
	notesURL := p.apiBase + "/projects/" + url.PathEscape(fullPath) +
		"/merge_requests/" + strconv.Itoa(iid) +
		"/notes?order_by=created_at&sort=desc&per_page=100"
	resp, err := p.do(ctx, notesURL)
	if err != nil {
		return nil
	}
	var notes []glNote
	if err := decodeJSON(resp, &notes); err != nil {
		return nil
	}
	return notes
}

// emitNewVerdictSignals applies the change-triggered verdict-signal logic for
// one MR. It computes the current verdict set from the GraphQL node, diffs it
// against the in-memory baseline, and — on first change — fetches page 1 of
// the MR's system notes to obtain the real provider timestamp for the new verdict.
//
// Design (ADR-0016 §2026-07-17):
//   - First sight of an MR (no baseline entry): store as history, emit nothing.
//     Pre-existing verdicts are already-known facts; they rank by updated_at.
//   - Changed/new verdict: one REST notes fetch for the MR; emit one event per
//     matched note with OccurredAt = note.created_at. Dedupe on note ID so an
//     unapprove + re-approve produces a new note and a new event.
//   - Notes fetch fails (HTTP error): emit nothing; leave the baseline entry
//     absent so the next cycle retries.
//   - Notes fetch succeeds but no matching system note on page 1: emit nothing,
//     update the baseline (give up; the verdict never advances the clock rather
//     than falling back to our poll clock).
//   - Actor disappears from the verdict set: evict from baseline so a re-verdict
//     counts as new.
//   - Draft MRs: no baseline, no emission — verdicts on a draft are suppressed
//     everywhere (applyGraphQL); a draft must not rank on a hidden approval.
func (p *Provider) emitNewVerdictSignals(
	ctx context.Context, nativeID, fullPath string, iid int, mr glGraphQLMR, draft bool,
) []sdk.Event {
	if draft {
		return nil
	}

	current := currentVerdictSet(mr)

	baseline, seen := p.verdictBaseline[nativeID]
	if !seen {
		// First sight: store as history, no emission.
		p.verdictBaseline[nativeID] = current
		return nil
	}

	// Evict actors that left the verdict set so a re-verdict is treated as new.
	for actor := range baseline {
		if _, still := current[actor]; !still {
			delete(baseline, actor)
		}
	}

	changed := changedVerdicts(current, baseline)
	if len(changed) == 0 {
		return nil
	}

	// One REST notes fetch covers all changed actors for this MR.
	notes := p.fetchVerdictNotes(ctx, fullPath, iid)
	if notes == nil {
		// HTTP error: leave baseline entries absent so next cycle retries.
		return nil
	}

	matched := matchVerdictNotes(notes, changed, current)

	var out []sdk.Event
	for _, actor := range changed {
		baseline[actor] = current[actor] // update regardless of match (give up if no note)
		n, ok := matched[actor]
		if !ok {
			continue // no matching note on page 1; baseline updated, no event
		}
		if ev, emit := verdictEvent(nativeID, actor, current[actor], n); emit {
			out = append(out, ev)
		}
	}
	p.verdictBaseline[nativeID] = current
	return out
}

// currentVerdictSet maps each actor with a current verdict to its normalized
// value (reviewStateApproved / reviewStateChangesRequested) from the GraphQL node.
func currentVerdictSet(mr glGraphQLMR) map[string]string {
	current := make(map[string]string, len(mr.ApprovedBy.Nodes)+len(mr.Reviewers.Nodes))
	for _, u := range mr.ApprovedBy.Nodes {
		current[u.Username] = reviewStateApproved
	}
	for _, r := range mr.Reviewers.Nodes {
		if r.Interaction.ReviewState == glReviewStateChangesRequested {
			current[r.Username] = reviewStateChangesRequested
		}
	}
	return current
}

// changedVerdicts returns actors whose current verdict differs from the baseline.
func changedVerdicts(current, baseline map[string]string) []string {
	var changed []string
	for actor, verdict := range current {
		if baseline[actor] != verdict {
			changed = append(changed, actor)
		}
	}
	return changed
}

// matchVerdictNotes scans notes newest-first and returns the first matching
// system note per changed actor (author + verdict-specific body).
func matchVerdictNotes(notes []glNote, changed []string, current map[string]string) map[string]*glNote {
	matched := make(map[string]*glNote, len(changed))
	for i := range notes {
		n := &notes[i]
		if !n.System {
			continue
		}
		for _, actor := range changed {
			if _, already := matched[actor]; already || n.Author.Username != actor {
				continue
			}
			wantBody := glNoteBodyApproved
			if current[actor] == reviewStateChangesRequested {
				wantBody = glNoteBodyChangesRequested
			}
			if n.Body == wantBody {
				matched[actor] = n
			}
		}
	}
	return matched
}

// verdictEvent builds the signal event for a matched note. emit is false when
// the note's created_at is unparsable (treated as no-match — never our clock).
func verdictEvent(nativeID, actor, verdict string, n *glNote) (ev sdk.Event, emit bool) {
	t := parseTime(n.CreatedAt)
	if t == nil {
		return sdk.Event{}, false
	}
	if verdict == reviewStateApproved {
		return sdk.Event{
			ObjectType: objectType,
			NativeID:   nativeID,
			EventType:  signalApproved,
			OccurredAt: t,
			Actor:      actor,
			DedupeKey:  signalApproved + ":note:" + strconv.Itoa(n.ID),
			Payload:    sdk.SignalApprovedPayload{Approver: actor},
		}, true
	}
	return sdk.Event{
		ObjectType: objectType,
		NativeID:   nativeID,
		EventType:  signalChangesRequested,
		OccurredAt: t,
		Actor:      actor,
		DedupeKey:  signalChangesRequested + ":note:" + strconv.Itoa(n.ID),
		Payload:    sdk.SignalChangesRequestedPayload{Reviewer: actor},
	}, true
}

func (p *Provider) graphqlJoin(ctx context.Context, events []sdk.Event) ([]sdk.Event, bool) {
	var items []glBatchItem
	for i, ev := range events {
		if ev.EventType != eventItemObserved {
			continue
		}
		pl, ok := ev.Payload.(sdk.ItemObservedPayload)
		if !ok {
			continue
		}
		fp, iid, ok := parseGLNativeID(ev.NativeID)
		if !ok {
			continue
		}
		items = append(items, glBatchItem{i, fp, iid, pl.Draft})
	}
	if len(items) == 0 {
		return events, false
	}

	gqlResults, degraded := p.runGraphQLBatches(ctx, items)

	if len(gqlResults) == 0 && !degraded {
		return events, false
	}

	enriched := make([]sdk.Event, len(events))
	copy(enriched, events)
	var verdicts []sdk.Event
	for _, it := range items {
		ev := enriched[it.evIdx]
		pl, ok := ev.Payload.(sdk.ItemObservedPayload)
		if !ok {
			continue
		}
		if mr, ok := gqlResults[it.evIdx]; ok {
			pl = applyGraphQL(pl, mr, p.handle)
			if pl.State == stateOpen {
				verdicts = append(verdicts, p.emitNewVerdictSignals(ctx, ev.NativeID, it.fullPath, it.iid, mr, pl.Draft)...)
			}
		} else if snap, ok := p.openSnapshots[ev.NativeID]; ok {
			// Batch for this item degraded: carry forward the last-known GraphQL-
			// enriched fields so a transient failure never downgrades good data.
			// No fresh verdict data, so no verdict signal — a prior cycle emitted it.
			pl = carryForwardEnrichment(pl, snap)
		}
		ev.Payload = pl
		ev.DedupeKey = observedDedupeKey(pl)
		enriched[it.evIdx] = ev
	}

	// Verdict signals ride after all input events, which keep their positions —
	// the reconcile reap derives its swept set from the item.observed native_ids
	// regardless of order, and the no-drop/no-reorder invariant is preserved (ADR-0024).
	out := make([]sdk.Event, 0, len(enriched)+len(verdicts))
	out = append(out, enriched...)
	out = append(out, verdicts...)
	return out, degraded
}

// carryForwardEnrichment merges the GraphQL-sourced fields from a prior snapshot
// onto pl so a failed batch does not zero out previously-known approval state.
// Boolean flags use OR so a known-bad state is never cleared by a stale snapshot.
// Draft suppression mirrors applyGraphQL: on an MR that has since become a draft
// the approval/review/gate fields are NOT carried (a draft hides them), so a
// stale non-draft snapshot cannot resurrect a "N approved" / needs-approval state
// on a now-draft MR. review_decision is not a merge-gate fact and is carried
// regardless of draft, matching applyGraphQL.
func carryForwardEnrichment(pl sdk.ItemObservedPayload, snap sdk.ItemObservedPayload) sdk.ItemObservedPayload {
	pl.ReviewDecision = snap.ReviewDecision
	if pl.Draft {
		return pl
	}
	pl.ApprovalsCount = snap.ApprovalsCount
	pl.MyReviewState = snap.MyReviewState
	pl.NeedsApproval = pl.NeedsApproval || snap.NeedsApproval
	pl.FailingChecks = pl.FailingChecks || snap.FailingChecks
	pl.ChecksRunning = pl.ChecksRunning || snap.ChecksRunning
	pl.NeedsRebase = pl.NeedsRebase || snap.NeedsRebase
	return pl
}

// applyGraphQL merges the GraphQL-derived booleans onto a payload.
// Draft items have NeedsApproval, NeedsRebase, FailingChecks, ChecksRunning,
// ApprovalsCount, and MyReviewState suppressed (forced false/zero/empty).
// MergeConflict is NOT draft-suppressed (REST records has_conflicts on drafts
// too; GraphQL conflicts keeps parity). handle is the authenticated user's
// username; MyReviewState records the user's own submitted verdict, derived
// from their reviewers.mergeRequestInteraction entry (changes_requested /
// reviewed / approved) and overridden to "approved" whenever they appear in
// approvedBy — approval is the authoritative merge-path verdict even if the
// interaction field lags.
func applyGraphQL(pl sdk.ItemObservedPayload, mr glGraphQLMR, handle string) sdk.ItemObservedPayload {
	if !pl.Draft {
		pl.NeedsApproval = !mr.Approved
		pl.NeedsRebase = mr.ShouldRebase || mr.Diverged
		pl.FailingChecks = isPipelineRed(mr.HeadPipeline)
		pl.ChecksRunning = isPipelineRunning(mr.HeadPipeline)
		pl.ApprovalsCount = mr.ApprovedBy.Count
		pl.MyReviewState = myReviewStateFromReviewers(mr, handle)
		for _, u := range mr.ApprovedBy.Nodes {
			if u.Username == handle {
				pl.MyReviewState = reviewStateApproved
				break
			}
		}
	}
	// MergeConflict is overridden from the GraphQL conflicts scalar regardless of
	// draft status — REST records has_conflicts on drafts too; keep parity.
	pl.MergeConflict = mr.Conflicts
	// review_decision drives the author's changes-requested chip; it is not a
	// merge-gate fact, so (like GitHub) it is recorded regardless of draft.
	pl.ReviewDecision = reviewDecisionFromReviewers(mr)
	return pl
}

// myReviewStateFromReviewers maps the authenticated user's own reviewer
// interaction to a normalized my_review_state. GitLab reports the viewer's
// per-MR verdict in reviewers.nodes.mergeRequestInteraction.reviewState; only
// the "done" verdicts (a submitted review) are normalized — pending states
// (UNREVIEWED, REVIEW_STARTED, ...) map to "" so the item stays review_requested.
func myReviewStateFromReviewers(mr glGraphQLMR, handle string) string {
	for _, r := range mr.Reviewers.Nodes {
		if r.Username != handle {
			continue
		}
		switch r.Interaction.ReviewState {
		case glReviewStateChangesRequested:
			return reviewStateChangesRequested
		case glReviewStateReviewed:
			return reviewStateReviewed
		case glReviewStateApproved:
			return reviewStateApproved
		}
		break
	}
	return ""
}

// reviewDecisionFromReviewers returns "changes_requested" when any reviewer's
// GraphQL reviewState is REQUESTED_CHANGES, else "" — GitLab has no single
// PR-level decision field, so the MR-level verdict is derived from its
// reviewers (the fold only consumes the changes-requested case).
func reviewDecisionFromReviewers(mr glGraphQLMR) string {
	for _, r := range mr.Reviewers.Nodes {
		if r.Interaction.ReviewState == glReviewStateChangesRequested {
			return decisionChangesRequested
		}
	}
	return ""
}

// openSetRefresh queries the volatile GraphQL fields for cached open items not
// already covered by todo-driven events, merges onto the cached payload, and
// appends item.observed events. On GraphQL failure it logs and skips the batch
// — the cycle still succeeds. Returns events and a degraded flag.
func (p *Provider) openSetRefresh(
	ctx context.Context, events []sdk.Event, covered map[string]bool,
) ([]sdk.Event, bool) {
	type openItem struct {
		nativeID string
		fullPath string
		iid      int
		payload  sdk.ItemObservedPayload
	}

	var openItems []openItem
	for nid, pl := range p.openSnapshots {
		if covered[nid] {
			continue
		}
		fp, iid, ok := parseGLNativeID(nid)
		if !ok {
			continue
		}
		openItems = append(openItems, openItem{nid, fp, iid, pl})
	}
	if len(openItems) == 0 {
		return events, false
	}

	queries := make([]mrBatchQuery, len(openItems))
	for i, it := range openItems {
		queries[i] = mrBatchQuery{it.fullPath, it.iid}
	}
	byIndex, degraded := p.runMRBatches(ctx, queries)

	// Iterate openItems in index order (not the map) so the appended events keep
	// a deterministic order.
	for i := range openItems {
		mr, ok := byIndex[i]
		if !ok {
			continue
		}
		it := openItems[i]
		pl := applyGraphQL(it.payload, mr, p.handle)
		events = append(events, sdk.Event{
			ObjectType: objectType,
			NativeID:   it.nativeID,
			EventType:  eventItemObserved,
			OccurredAt: parseTime(pl.ProviderUpdatedAt),
			Actor:      pl.Author,
			DedupeKey:  observedDedupeKey(pl),
			Payload:    pl,
		})
		// openSnapshots holds only open items; draft suppression is inside emitNewVerdictSignals.
		events = append(events, p.emitNewVerdictSignals(ctx, it.nativeID, it.fullPath, it.iid, mr, pl.Draft)...)
	}
	return events, degraded
}

// isPipelineRed reports whether the pipeline status represents a failure.
func isPipelineRed(pip *glPipeline) bool {
	if pip == nil {
		return false
	}
	switch pip.Status {
	case "FAILED", "CANCELED":
		return true
	default:
		return false
	}
}

// isPipelineRunning reports whether the pipeline is in progress — queued,
// preparing, or executing — per the GitLab GraphQL PipelineStatusEnum. Terminal
// statuses (SUCCESS, FAILED, CANCELED, SKIPPED) and MANUAL (awaiting a manual
// job) are not running.
func isPipelineRunning(pip *glPipeline) bool {
	if pip == nil {
		return false
	}
	switch pip.Status {
	case "RUNNING", "PENDING", "CREATED", "WAITING_FOR_RESOURCE", "PREPARING", "SCHEDULED":
		return true
	default:
		return false
	}
}

// parseGLNativeID splits "group/project!iid" into its components.
// Returns ok=false for numeric-only project IDs (no References.Full fallback).
func parseGLNativeID(nid string) (fullPath string, iid int, ok bool) {
	bangIdx := strings.LastIndex(nid, "!")
	if bangIdx < 0 || bangIdx+1 >= len(nid) {
		return
	}
	n, err := strconv.Atoi(nid[bangIdx+1:])
	if err != nil {
		return
	}
	path := nid[:bangIdx]
	if _, err := strconv.Atoi(path); err == nil {
		return
	}
	return path, n, true
}
