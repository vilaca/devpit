package github

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/vilaca/devpit/sdk"
)

const prQueryFmt = `a%d:repository(owner:"%s",name:"%s")` +
	`{pullRequest(number:%d){reviewDecision latestReviews{nodes{state author{login}}} autoMergeRequest{enabledAt}}}`

const (
	ghReviewStateApproved = "APPROVED"
	normalizedApproved    = "approved"
	// GitHub review-state / reviewDecision enum values and their normalized forms.
	ghChangesRequested         = "CHANGES_REQUESTED"
	ghReviewRequired           = "REVIEW_REQUIRED"
	normalizedChangesRequested = "changes_requested"
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
}

type ghPRNode struct {
	PullRequest struct {
		ReviewDecision string `json:"reviewDecision"`
		LatestReviews  struct {
			Nodes []struct {
				State  string `json:"state"`
				Author struct {
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

func mergeGHBatchResults(data map[string]json.RawMessage, batch []prItem, results map[int]ghResult, handle string) {
	for j, it := range batch {
		raw, ok := data[fmt.Sprintf("a%d", j)]
		if !ok || raw == nil {
			continue
		}
		var node ghPRNode
		if json.Unmarshal(raw, &node) != nil {
			continue
		}
		count := 0
		var myReviewState string
		for _, r := range node.PullRequest.LatestReviews.Nodes {
			if r.State == ghReviewStateApproved {
				count++
			}
			if r.Author.Login == handle {
				myReviewState = ghReviewState(r.State)
			}
		}
		results[it.evIdx] = ghResult{
			node.PullRequest.ReviewDecision, count,
			node.PullRequest.AutoMergeRequest != nil, myReviewState,
		}
	}
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

// doGraphQL POSTs a GraphQL query to the GitHub GraphQL API and returns the "data" map.
// On non-2xx it returns a classified SDK error. Graceful-degradation callers catch errors and continue.
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
	return result.Data, nil
}

// graphqlJoin enriches item.observed events with GitHub GraphQL data
// (reviewDecision, approvals, and auto-merge state).
// On any GraphQL error it logs and returns the original events unchanged (graceful degradation).
// Invariant: it never drops or reorders events — every input event appears in the output,
// enriched or verbatim. The engine relies on this to derive the reconcile swept set from
// the result's events (ADR-0024); a future edit must preserve it.
// NeedsApproval is set only when reviewDecision == "REVIEW_REQUIRED" && !draft && gate == blocked,
// avoiding the "ready to merge · missing approvals" contradiction caused by timing skew or drafts.
// AutoMergeArmed is set from autoMergeRequest (non-null ⇒ armed); it degrades to false when the
// field is unreadable or GraphQL fails. ChecksRunning is left false on GitHub (documented parity
// gap: a gating in-progress pipeline is hidden inside the blocked gate, ADR-0016).
func (p *Provider) graphqlJoin(ctx context.Context, events []sdk.Event) []sdk.Event {
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
		return events
	}

	results := make(map[int]ghResult, len(items))

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
			log.Printf("devpit: github graphql join degraded: %v", err)
			continue
		}

		mergeGHBatchResults(data, batch, results, p.handle)
	}

	if len(results) == 0 {
		return events
	}

	// Build a lookup from evIdx → "owner/repo" for the opportunistic downgrade below.
	evIdxToRepo := make(map[int]string, len(items))
	for _, it := range items {
		evIdxToRepo[it.evIdx] = it.owner + "/" + it.repo
	}

	out := make([]sdk.Event, len(events))
	copy(out, events)
	for evIdx, r := range results {
		ev := out[evIdx]
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
		out[evIdx] = ev
	}
	return out
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
