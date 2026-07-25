package gitlab

import (
	"context"
	"maps"
	"net/url"
	"time"

	"github.com/vilaca/devpit/sdk"
)

// reconcileQueries defines the URL query strings for each reconcile sweep.
// GitLab's global /merge_requests endpoint accepts scope=assigned_to_me and
// scope=created_by_me directly; scope=reviewer returns 400. Reviewer MRs need
// reviewer_username together with scope=all — the endpoint otherwise defaults
// to scope=created_by_me, which filters out MRs the user reviews but did not
// author (so reviewer_username alone returns nothing).
func (p *Provider) reconcileQueries() []string {
	return []string{
		"scope=assigned_to_me",
		"scope=created_by_me",
		"scope=all&reviewer_username=" + url.QueryEscape(p.handle),
	}
}

func cursorRecQuery(q string) string { return "gl.rec.updated_after." + q }

// Reconcile implements sdk.Provider: it sweeps the user's involved merge
// requests across the reconcile queries and emits item.observed snapshots.
func (p *Provider) Reconcile(ctx context.Context, state sdk.PollState) (sdk.PollResult, error) {
	if state == nil {
		state = sdk.PollState{}
	}
	out := sdk.PollState{}
	maps.Copy(out, state)

	now := time.Now().UTC().Format(time.RFC3339)

	seen := map[string]bool{}
	var events []sdk.Event
	var rate *int

	queries := p.reconcileQueries()
	for _, q := range queries {
		updatedAfter := state[cursorRecQuery(q)]
		base := p.apiBase + "/merge_requests?" + q + "&state=opened&per_page=100"
		if updatedAfter != "" {
			base += "&updated_after=" + url.QueryEscape(updatedAfter)
		}
		// Follow GitLab's X-Next-Page cursor so a query with more than one page
		// is not silently truncated to the first 100 MRs.
		for u := base; u != ""; {
			resp, err := p.do(ctx, u)
			if err != nil {
				return sdk.PollResult{}, err
			}
			if r := rateRemaining(resp.Header); r != nil {
				rate = r
			}
			next := resp.Header.Get("X-Next-Page")
			var mrs []glMergeRequest
			if err := decodeJSON(resp, &mrs); err != nil {
				return sdk.PollResult{}, err
			}
			for _, mr := range mrs {
				if seen[mr.WebURL] {
					continue
				}
				seen[mr.WebURL] = true
				events = append(events, p.observedFromMR(mr))
			}
			if next == "" {
				break
			}
			u = base + "&page=" + next
		}
	}

	var degraded bool
	events, degraded = p.graphqlJoin(ctx, events)

	// Advance the per-scope cursors only when this cycle's enrichment succeeded.
	// graphqlJoin is a single cross-scope batch, so a degraded join means no
	// scope's items were reliably enriched — hold every cursor at its prior value
	// (out starts as a copy of state) so the next reconcile re-fetches and retries.
	// Advancing here would assert those items are current when they were never
	// enriched, locking in stale approval/pipeline/rebase state.
	if !degraded {
		for _, q := range queries {
			out[cursorRecQuery(q)] = now
		}
	}

	// Merge the freshly-joined snapshots into the open-set cache so FastPoll's
	// open-set refresh always starts from a full REST+GraphQL payload.
	for _, ev := range events {
		if ev.EventType != eventItemObserved {
			continue
		}
		if pl, ok := ev.Payload.(sdk.ItemObservedPayload); ok {
			p.openSnapshots[ev.NativeID] = pl
		}
	}
	return sdk.PollResult{
		Events:        events,
		State:         out,
		RateRemaining: rate,
		ItemsChanged:  len(events),
		Degraded:      degraded,
	}, nil
}
