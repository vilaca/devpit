package gitlab

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/vilaca/devpit/sdk"
)

const mrQueryFmt = `a%d:project(fullPath:"%s")` +
	`{mergeRequest(iid:"%d"){approved shouldBeRebased headPipeline{status} approvedBy{count nodes{username}}}}`

// doGraphQL POSTs a GraphQL query to the GitLab GraphQL API and returns the "data" map.
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
	return result.Data, nil
}

type glPipeline struct {
	Status string `json:"status"`
}

// glGraphQLMR holds the GraphQL join result for one GitLab MR.
type glGraphQLMR struct {
	Approved     bool        `json:"approved"`
	ShouldRebase bool        `json:"shouldBeRebased"`
	HeadPipeline *glPipeline `json:"headPipeline"`
	ApprovedBy   struct {
		Count int `json:"count"`
		Nodes []struct {
			Username string `json:"username"`
		} `json:"nodes"`
	} `json:"approvedBy"`
}

// graphqlJoin enriches item.observed events with GitLab GraphQL data.
// On failure it logs and returns original events (graceful degradation).
// Draft suppression: all GraphQL-joined booleans (NeedsApproval, NeedsRebase, FailingChecks)
// are set to false for draft MRs.
func (p *Provider) graphqlJoin(ctx context.Context, events []sdk.Event) []sdk.Event {
	type mrItem struct {
		evIdx    int
		fullPath string
		iid      int
		draft    bool
	}

	var items []mrItem
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
		items = append(items, mrItem{i, fp, iid, pl.Draft})
	}
	if len(items) == 0 {
		return events
	}

	gqlResults := make(map[int]glGraphQLMR, len(items))

	const batchSize = 30
	for start := 0; start < len(items); start += batchSize {
		batch := items[start:min(start+batchSize, len(items))]

		var q strings.Builder
		q.WriteString("query{")
		for j, it := range batch {
			fmt.Fprintf(&q, mrQueryFmt, j, it.fullPath, it.iid)
		}
		q.WriteString("}")

		data, err := p.doGraphQL(ctx, q.String())
		if err != nil {
			log.Printf("devpit: gitlab graphql join degraded: %v", err)
			continue
		}

		for j, it := range batch {
			raw, ok := data[fmt.Sprintf("a%d", j)]
			if !ok || raw == nil {
				continue
			}
			var node struct {
				MergeRequest *glGraphQLMR `json:"mergeRequest"`
			}
			if json.Unmarshal(raw, &node) == nil && node.MergeRequest != nil {
				gqlResults[it.evIdx] = *node.MergeRequest
			}
		}
	}

	if len(gqlResults) == 0 {
		return events
	}

	out := make([]sdk.Event, len(events))
	copy(out, events)
	for evIdx, mr := range gqlResults {
		ev := out[evIdx]
		pl, ok := ev.Payload.(sdk.ItemObservedPayload)
		if !ok {
			continue
		}
		pl = applyGraphQL(pl, mr, p.handle)
		ev.Payload = pl
		ev.DedupeKey = observedDedupeKey(pl)
		out[evIdx] = ev
	}
	return out
}

// applyGraphQL merges the GraphQL-derived booleans onto a payload.
// Draft items keep all these booleans false (draft suppression).
// handle is the authenticated user's username; when it appears in approvedBy the
// payload records MyReviewState "approved" (GitLab exposes no cheap per-user
// state for comment-only reviews, so only approval is detected here).
func applyGraphQL(pl sdk.ItemObservedPayload, mr glGraphQLMR, handle string) sdk.ItemObservedPayload {
	if !pl.Draft {
		pl.NeedsApproval = !mr.Approved
		pl.NeedsRebase = mr.ShouldRebase
		pl.FailingChecks = isPipelineRed(mr.HeadPipeline)
		pl.ChecksRunning = isPipelineRunning(mr.HeadPipeline)
		pl.ApprovalsCount = mr.ApprovedBy.Count
		for _, u := range mr.ApprovedBy.Nodes {
			if u.Username == handle {
				pl.MyReviewState = "approved"
				break
			}
		}
	}
	return pl
}

// openSetRefresh queries the three volatile GraphQL booleans for cached open
// items not already covered by todo-driven events, merges onto the cached
// payload, and appends item.observed events to events. On GraphQL failure it
// logs to sync_log and skips the batch — the cycle still succeeds.
func (p *Provider) openSetRefresh(ctx context.Context, events []sdk.Event, covered map[string]bool) []sdk.Event {
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
		return events
	}

	const batchSize = 30
	for start := 0; start < len(openItems); start += batchSize {
		batch := openItems[start:min(start+batchSize, len(openItems))]

		var q strings.Builder
		q.WriteString("query{")
		for j, it := range batch {
			fmt.Fprintf(&q, mrQueryFmt, j, it.fullPath, it.iid)
		}
		q.WriteString("}")

		data, err := p.doGraphQL(ctx, q.String())
		if err != nil {
			log.Printf("devpit: gitlab graphql open-set refresh degraded: %v", err)
			continue
		}

		for j, it := range batch {
			raw, ok := data[fmt.Sprintf("a%d", j)]
			if !ok || raw == nil {
				continue
			}
			var node struct {
				MergeRequest *glGraphQLMR `json:"mergeRequest"`
			}
			if json.Unmarshal(raw, &node) != nil || node.MergeRequest == nil {
				continue
			}
			pl := applyGraphQL(it.payload, *node.MergeRequest, p.handle)
			events = append(events, sdk.Event{
				ObjectType: objectType,
				NativeID:   it.nativeID,
				EventType:  eventItemObserved,
				OccurredAt: parseTime(pl.ProviderUpdatedAt),
				Actor:      pl.Author,
				DedupeKey:  observedDedupeKey(pl),
				Payload:    pl,
			})
		}
	}
	return events
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
