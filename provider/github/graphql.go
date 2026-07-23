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

	"github.com/vilaca/devpit/sdk"
)

const prQueryFmt = `a%d:repository(owner:"%s",name:"%s")` +
	`{pullRequest(number:%d){reviewDecision latestReviews{nodes{state}}}}`

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
}

type ghPRNode struct {
	PullRequest struct {
		ReviewDecision string `json:"reviewDecision"`
		LatestReviews  struct {
			Nodes []struct {
				State string `json:"state"`
			} `json:"nodes"`
		} `json:"latestReviews"`
	} `json:"pullRequest"`
}

func mergeGHBatchResults(data map[string]json.RawMessage, batch []prItem, results map[int]ghResult) {
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
		for _, r := range node.PullRequest.LatestReviews.Nodes {
			if r.State == "APPROVED" {
				count++
			}
		}
		results[it.evIdx] = ghResult{node.PullRequest.ReviewDecision, count}
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

// graphqlJoin enriches item.observed events with GitHub GraphQL data (reviewDecision).
// On any GraphQL error it logs and returns the original events unchanged (graceful degradation).
// NeedsApproval is set only when reviewDecision == "REVIEW_REQUIRED" && !draft && gate == blocked,
// avoiding the "ready to merge · missing approvals" contradiction caused by timing skew or drafts.
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

		mergeGHBatchResults(data, batch, results)
	}

	if len(results) == 0 {
		return events
	}

	out := make([]sdk.Event, len(events))
	copy(out, events)
	for evIdx, r := range results {
		ev := out[evIdx]
		pl, ok := ev.Payload.(sdk.ItemObservedPayload)
		if !ok {
			continue
		}
		pl.NeedsApproval = r.reviewDecision == "REVIEW_REQUIRED" && !pl.Draft && pl.Gate == gateBlocked
		pl.ApprovalsCount = r.approvalsCount
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
