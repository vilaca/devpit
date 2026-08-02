package github

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/vilaca/devpit/sdk"
)

const prQueryFmt = `a%d:repository(owner:"%s",name:"%s")` +
	`{pullRequest(number:%d){reviewDecision ` +
	`latestReviews{nodes{state submittedAt author{login}}} autoMergeRequest{enabledAt}}}`

const (
	ghReviewStateApproved = "APPROVED"
	normalizedApproved    = "approved"
	// GitHub review-state / reviewDecision enum values and their normalized forms.
	ghChangesRequested         = "CHANGES_REQUESTED"
	ghReviewRequired           = "REVIEW_REQUIRED"
	normalizedChangesRequested = "changes_requested"
)

// Rank-advancing review-verdict signal event types (duplicated from the GitLab
// provider on purpose — providers share no helpers, ADR-0003).
const (
	signalApproved         = "signal.approved"
	signalChangesRequested = "signal.changes_requested"
)

type prItem struct {
	evIdx  int
	owner  string
	repo   string
	number int
	draft  bool
	gate   string
}

type ghResult struct {
	reviewDecision string
	approvalsCount int
	autoMergeArmed bool
	myReviewState  string
	verdictSigs    []sdk.Event // verdict signals to append after the enriched events
}

type ghPRNode struct {
	// PullRequest is a pointer so an aliased repository/pullRequest that resolves
	// to JSON null (inaccessible repo, deleted PR) reads as nil instead of a zero
	// struct — the caller then keeps the REST payload rather than overwriting it
	// with zeros (A3).
	PullRequest *struct {
		ReviewDecision string `json:"reviewDecision"`
		LatestReviews  struct {
			Nodes []struct {
				State       string `json:"state"`
				SubmittedAt string `json:"submittedAt"`
				Author      struct {
					Login string `json:"login"`
				} `json:"author"`
			} `json:"nodes"`
		} `json:"latestReviews"`
		// autoMergeRequest is null unless auto-merge is armed on the PR; a
		// non-null object means AutoMergeArmed. Pointer so absent/null reads as
		// nil (a fine-grained PAT that cannot read it degrades to false, no crash).
		AutoMergeRequest *struct {
			EnabledAt string `json:"enabledAt"`
		} `json:"autoMergeRequest"`
	} `json:"pullRequest"`
}

// mergeGHBatchResults applies each aliased PR node onto results, keyed by the
// item's event index. It returns true when any node was missing or JSON-null: a
// null node unmarshals into a zero struct, so guarding on node presence stops an
// inaccessible repo in the batch from overwriting a PR's good REST enrichment
// with zeros (A3). A missing/null node is skipped — its REST payload survives —
// and reported as degraded so the cycle records a degraded outcome (A2).
func mergeGHBatchResults(
	data map[string]json.RawMessage, batch []prItem, results map[int]ghResult, handle string,
) bool {
	var degraded bool
	for j, it := range batch {
		raw, ok := data[fmt.Sprintf("a%d", j)]
		if !ok || raw == nil {
			degraded = true
			continue
		}
		var node ghPRNode
		if json.Unmarshal(raw, &node) != nil || node.PullRequest == nil {
			degraded = true
			continue
		}
		count := 0
		var myReviewState string
		var verdictSigs []sdk.Event
		nativeID := it.owner + "/" + it.repo + "#" + strconv.Itoa(it.number)
		for _, r := range node.PullRequest.LatestReviews.Nodes {
			if r.State == ghReviewStateApproved {
				count++
			}
			if r.Author.Login == handle {
				myReviewState = ghReviewState(r.State)
			}
			// Emit verdict signals for non-draft PRs with a real provider timestamp.
			// COMMENTED and DISMISSED states carry no verdict ranking signal.
			// Skip nodes without a parsable submittedAt — never fall back to our clock.
			if !it.draft && r.SubmittedAt != "" {
				t := parseTime(r.SubmittedAt)
				if t == nil {
					continue
				}
				switch r.State {
				case ghReviewStateApproved:
					verdictSigs = append(verdictSigs, sdk.Event{
						ObjectType: objectType,
						NativeID:   nativeID,
						EventType:  signalApproved,
						OccurredAt: t,
						Actor:      r.Author.Login,
						DedupeKey:  signalApproved + ":review:" + r.Author.Login + ":" + r.SubmittedAt,
						Payload:    sdk.SignalApprovedPayload{Approver: r.Author.Login},
					})
				case ghChangesRequested:
					verdictSigs = append(verdictSigs, sdk.Event{
						ObjectType: objectType,
						NativeID:   nativeID,
						EventType:  signalChangesRequested,
						OccurredAt: t,
						Actor:      r.Author.Login,
						DedupeKey:  signalChangesRequested + ":review:" + r.Author.Login + ":" + r.SubmittedAt,
						Payload:    sdk.SignalChangesRequestedPayload{Reviewer: r.Author.Login},
					})
				}
			}
		}
		results[it.evIdx] = ghResult{
			node.PullRequest.ReviewDecision, count,
			node.PullRequest.AutoMergeRequest != nil, myReviewState,
			verdictSigs,
		}
	}
	return degraded
}

// ghReviewDecision maps GitHub's PR-level reviewDecision (the
// PullRequestReviewDecision enum) to the fold's review_decision vocabulary.
// Only "changes_requested" drives a chip today (the author's changes-requested
// signal); the other verdicts are normalized through so the fact stays honest.
// An empty/unknown decision (e.g. a PAT that cannot read it) reads as "".
func ghReviewDecision(decision string) string {
	switch decision {
	case ghChangesRequested:
		return normalizedChangesRequested
	case ghReviewStateApproved:
		return normalizedApproved
	case ghReviewRequired:
		return "review_required"
	default:
		return ""
	}
}

// ghReviewState maps a GitHub review state to a normalized my_review_state
// value. Only submitted verdicts count as a completed review; PENDING/DISMISSED
// leave it empty.
func ghReviewState(state string) string {
	switch state {
	case ghReviewStateApproved:
		return normalizedApproved
	case ghChangesRequested:
		return normalizedChangesRequested
	case "COMMENTED":
		return "reviewed"
	default:
		return ""
	}
}

// ghErrRateLimited is the GitHub GraphQL error `type` for both primary and
// secondary rate limiting, delivered as HTTP 200 with an errors array.
const ghErrRateLimited = "RATE_LIMITED"

// graphQLError is returned by doGraphQL when GitHub responds HTTP 200 with an
// errors array and no data that is not a rate limit — e.g. a query-complexity
// rejection. The caller records a degraded outcome rather than silently treating
// every node as missing. Duplicated from the GitLab provider on purpose
// (ADR-0003): providers never share helpers.
type graphQLError struct {
	msg string
}

func (e *graphQLError) Error() string { return "github graphql: " + e.msg }

// doGraphQL POSTs a GraphQL query to the GitHub GraphQL API and returns the "data" map.
// On non-2xx it returns a classified SDK error. GitHub also signals GraphQL-level
// failures as HTTP 200 with an errors array (complexity ceilings and secondary
// rate limiting both arrive this way); those are classified too — a rate limit
// surfaces sdk.ErrRateLimited so the engine backs off, anything else a
// *graphQLError so the caller degrades. Graceful-degradation callers catch the
// non-rate errors and continue.
func (p *Provider) doGraphQL(ctx context.Context, query string) (map[string]json.RawMessage, error) {
	body, _ := json.Marshal(struct { //nolint:errchkjson // struct has no interface fields; Marshal cannot fail
		Query string `json:"query"`
	}{query})

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.graphqlEndpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "bearer "+p.cfg.Token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", userAgent)

	resp, err := p.http.Do(req)
	if err != nil {
		return nil, err
	}

	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		// proceed to decode
	case resp.StatusCode == http.StatusUnauthorized:
		_ = resp.Body.Close()
		return nil, sdk.ErrUnauthorized
	case resp.StatusCode == http.StatusTooManyRequests:
		d := parseRateDelay(resp)
		_ = resp.Body.Close()
		return nil, &sdk.RateLimitError{RetryAfter: d}
	case resp.StatusCode == http.StatusForbidden:
		// A 403 is a rate limit only with rate signals present; otherwise it is a
		// permission/SSO/scope denial that must surface, not retry forever (A4).
		if isRateLimited(resp) {
			d := parseRateDelay(resp)
			_ = resp.Body.Close()
			return nil, &sdk.RateLimitError{RetryAfter: d}
		}
		_ = resp.Body.Close()
		return nil, sdk.ErrUnauthorized
	default:
		_ = resp.Body.Close()
		return nil, &sdk.StatusError{Status: resp.StatusCode}
	}

	var result struct {
		Data   map[string]json.RawMessage `json:"data"`
		Errors []struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := decodeJSON(resp, &result); err != nil {
		return nil, err
	}

	// GitHub delivers GraphQL-level failures as HTTP 200 + an errors array.
	if len(result.Errors) > 0 {
		for _, e := range result.Errors {
			if e.Type == ghErrRateLimited {
				return nil, sdk.ErrRateLimited
			}
		}
		// Partial data (some aliases resolved, others errored to null) is still
		// worth applying: return it so the good nodes enrich and the null-node
		// guard in mergeGHBatchResults skips-and-degrades the rest (A3).
		if result.Data != nil {
			return result.Data, nil
		}
		msg := "server returned errors with no data"
		if result.Errors[0].Message != "" {
			msg = result.Errors[0].Message
		}
		return nil, &graphQLError{msg: msg}
	}
	if result.Data == nil {
		return nil, &graphQLError{msg: "server returned no data"}
	}
	return result.Data, nil
}

// runGHBatches queries the join fields for items in batches of batchSize (kept
// well within the GraphQL node budget) and returns the per-evIdx results, a
// degraded flag (any batch failed or any node came back null), and an error only
// for a rate limit — that is propagated so the engine backs off, while every
// other GraphQL failure logs, degrades, and keeps the batch's REST payload.
func (p *Provider) runGHBatches(ctx context.Context, items []prItem) (map[int]ghResult, bool, error) {
	results := make(map[int]ghResult, len(items))
	var degraded bool
	const batchSize = 30
	for start := 0; start < len(items); start += batchSize {
		batch := items[start:min(start+batchSize, len(items))]

		var q strings.Builder
		q.WriteString("query{")
		for j, it := range batch {
			fmt.Fprintf(&q, prQueryFmt, j, it.owner, it.repo, it.number)
		}
		q.WriteString("}")

		data, err := p.doGraphQL(ctx, q.String())
		if err != nil {
			if errors.Is(err, sdk.ErrRateLimited) {
				return nil, false, err
			}
			log.Printf("devpit: github graphql join degraded: %v", err)
			degraded = true
			continue
		}
		if mergeGHBatchResults(data, batch, results, p.handle) {
			degraded = true
		}
	}
	return results, degraded, nil
}

// graphqlJoin enriches item.observed events with GitHub GraphQL data
// (reviewDecision, approvals, and auto-merge state). It returns the events, a
// degraded flag (true when any batch failed or any node came back null), and an
// error only for a rate limit — a rate-limit signal is propagated so the engine
// backs off, while every other GraphQL failure logs, degrades, and keeps the
// batch's REST payload (A1/A2), since REST data is still authoritative.
// Invariant: it never drops or reorders events — every input event appears in the output,
// enriched or verbatim. The engine relies on this to derive the reconcile swept set from
// the result's events (ADR-0024); a future edit must preserve it.
// NeedsApproval is set only when reviewDecision == "REVIEW_REQUIRED" && !draft && gate == blocked,
// avoiding the "ready to merge · missing approvals" contradiction caused by timing skew or drafts.
// AutoMergeArmed is set from autoMergeRequest (non-null ⇒ armed); it degrades to false when the
// field is unreadable or GraphQL fails. ChecksRunning is left false on GitHub (documented parity
// gap: a gating in-progress pipeline is hidden inside the blocked gate, ADR-0016).
func (p *Provider) graphqlJoin(ctx context.Context, events []sdk.Event) ([]sdk.Event, bool, error) {
	var items []prItem
	for i, ev := range events {
		if ev.EventType != eventItemObserved {
			continue
		}
		pl, ok := ev.Payload.(sdk.ItemObservedPayload)
		if !ok {
			continue
		}
		owner, repo, number, ok := parseGHNativeID(ev.NativeID)
		if !ok {
			continue
		}
		items = append(items, prItem{i, owner, repo, number, pl.Draft, pl.Gate})
	}
	if len(items) == 0 {
		return events, false, nil
	}

	results, degraded, err := p.runGHBatches(ctx, items)
	if err != nil {
		return events, false, err
	}
	if len(results) == 0 {
		return events, degraded, nil
	}

	// Build a lookup from evIdx → "owner/repo" for the opportunistic downgrade below.
	evIdxToRepo := make(map[int]string, len(items))
	for _, it := range items {
		evIdxToRepo[it.evIdx] = it.owner + "/" + it.repo
	}

	enriched := make([]sdk.Event, len(events))
	copy(enriched, events)
	var verdicts []sdk.Event
	for evIdx, r := range results {
		ev := enriched[evIdx]
		pl, ok := ev.Payload.(sdk.ItemObservedPayload)
		if !ok {
			continue
		}
		pl.NeedsApproval = r.reviewDecision == ghReviewRequired && !pl.Draft && pl.Gate == gateBlocked
		pl.ReviewDecision = ghReviewDecision(r.reviewDecision)
		pl.ApprovalsCount = r.approvalsCount
		pl.AutoMergeArmed = r.autoMergeArmed
		pl.MyReviewState = r.myReviewState

		// Opportunistic downgrade: if approvals exist beyond the user's own,
		// another account can approve — mark the repo as not-sole-approver immediately.
		if repoKey, keyOK := evIdxToRepo[evIdx]; keyOK {
			myCount := 0
			if r.myReviewState == normalizedApproved {
				myCount = 1
			}
			if r.approvalsCount > myCount {
				p.approverCache[repoKey] = approverEntry{isSole: false, fetchedAt: time.Now()}
			}
		}

		ev.Payload = pl
		ev.DedupeKey = observedDedupeKey(pl)
		enriched[evIdx] = ev

		// Verdict signals ride after all input events (ADR-0024). Only emit for open
		// non-draft PRs; r.verdictSigs is already filtered to non-draft in mergeGHBatchResults.
		if pl.State == stateOpen {
			verdicts = append(verdicts, r.verdictSigs...)
		}
	}

	// Verdict signals ride after the enriched item.observed events, which keep
	// their positions — the reconcile reap derives its swept set regardless of
	// order, preserving the no-drop/no-reorder invariant (ADR-0024).
	out := make([]sdk.Event, 0, len(enriched)+len(verdicts))
	out = append(out, enriched...)
	out = append(out, verdicts...)
	return out, degraded, nil
}

// parseGHNativeID splits "owner/repo#number" into its components.
func parseGHNativeID(nid string) (owner, repo string, number int, ok bool) {
	hashIdx := strings.LastIndex(nid, "#")
	if hashIdx < 0 || hashIdx+1 >= len(nid) {
		return
	}
	n, err := parseInt(nid[hashIdx+1:])
	if err != nil {
		return
	}
	ownerRepo := nid[:hashIdx]
	o, r, found := strings.Cut(ownerRepo, "/")
	if !found {
		return
	}
	return o, r, n, true
}

func parseInt(s string) (int, error) {
	if len(s) == 0 {
		return 0, errors.New("empty")
	}
	var n int
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("not a digit: %c", c)
		}
		n = n*10 + int(c-'0')
	}
	return n, nil
}
