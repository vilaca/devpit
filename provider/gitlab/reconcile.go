package gitlab

import (
	"context"
	"fmt"
	"log"
	"maps"
	"net/url"
	"sort"
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

const roleSoleApprover = "sole_approver"

func cursorRecQuery(q string) string { return "gl.rec.updated_after." + q }

// fetchScopeMRs fetches all opened MRs for one reconcile scope, following
// GitLab's X-Next-Page cursor so a multi-page query is not silently truncated
// to the first 100 MRs. It emits an item.observed event per MR not already in
// seen (dedupe across scopes) and returns those events plus the latest
// rate-remaining header value observed.
func (p *Provider) fetchScopeMRs(ctx context.Context, base string, seen map[string]bool) ([]sdk.Event, *int, error) {
	var events []sdk.Event
	var rate *int
	for u := base; u != ""; {
		resp, err := p.do(ctx, u)
		if err != nil {
			return nil, nil, err
		}
		if r := rateRemaining(resp.Header); r != nil {
			rate = r
		}
		next := resp.Header.Get("X-Next-Page")
		var mrs []glMergeRequest
		if err := decodeJSON(resp, &mrs); err != nil {
			return nil, nil, err
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
	return events, rate, nil
}

// fetchSoleApproverMRs discovers open MRs on projects where the user is the
// only merge-capable member (access_level >= 40) and emits item.observed events
// with the sole_approver role. Drafts and self-authored MRs are skipped.
func (p *Provider) fetchSoleApproverMRs(ctx context.Context, seen map[string]bool) []sdk.Event {
	projects, err := p.fetchOwnedProjects(ctx)
	if err != nil {
		log.Printf("devpit: gitlab: owned-projects fetch degraded: %v", err)
		return nil
	}

	var events []sdk.Event
	for _, proj := range projects {
		isSole, err := p.isSoleApproverCached(ctx, proj.PathWithNamespace)
		if err != nil || !isSole {
			continue
		}
		evs := p.fetchProjectSoleApproverMRs(ctx, proj, seen)
		events = append(events, evs...)
	}
	return events
}

// fetchProjectSoleApproverMRs fetches all open MRs for a single project and
// returns item.observed events with the sole_approver role appended. Drafts
// and self-authored MRs are skipped; seen is used for cross-scope dedup.
func (p *Provider) fetchProjectSoleApproverMRs(ctx context.Context, proj glProject, seen map[string]bool) []sdk.Event {
	base := fmt.Sprintf("%s/projects/%d/merge_requests?state=opened&scope=all&per_page=100", p.apiBase, proj.ID)
	var events []sdk.Event
	for u := base; u != ""; {
		resp, err := p.do(ctx, u)
		if err != nil {
			log.Printf("devpit: gitlab: sole-approver MR fetch degraded: %v", err)
			break
		}
		next := resp.Header.Get("X-Next-Page")
		var mrs []glMergeRequest
		if err := decodeJSON(resp, &mrs); err != nil {
			log.Printf("devpit: gitlab: sole-approver MR decode degraded: %v", err)
			break
		}
		for _, mr := range mrs {
			if mr.Draft || mr.Author.Username == p.handle || seen[mr.WebURL] {
				continue
			}
			seen[mr.WebURL] = true
			ev := p.observedFromMR(mr)
			if pl, ok := ev.Payload.(sdk.ItemObservedPayload); ok {
				pl.MyRoles = append(pl.MyRoles, roleSoleApprover)
				sort.Strings(pl.MyRoles)
				ev.Payload = pl
				ev.DedupeKey = observedDedupeKey(pl)
			}
			events = append(events, ev)
		}
		if next == "" {
			break
		}
		u = fmt.Sprintf("%s/projects/%d/merge_requests?state=opened&scope=all&per_page=100&page=%s",
			p.apiBase, proj.ID, next)
	}
	return events
}

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
		scopeEvents, scopeRate, err := p.fetchScopeMRs(ctx, base, seen)
		if err != nil {
			return sdk.PollResult{}, err
		}
		events = append(events, scopeEvents...)
		if scopeRate != nil {
			rate = scopeRate
		}
	}

	// Sole-approver discovery: MRs on projects where the user is the only
	// merge-capable member. Uses the shared seen map to deduplicate against the
	// regular scopes above.
	soleEvents := p.fetchSoleApproverMRs(ctx, seen)
	events = append(events, soleEvents...)

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
