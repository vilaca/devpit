package github

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/vilaca/devpit/sdk"
)

const roleSoleApprover = "sole_approver"

// searchScope pairs a query qualifier with the role it implies for me.
type searchScope struct {
	qualifier string
	role      string
}

var reconcileScopes = []searchScope{
	{"review-requested", "reviewer"},
	{"assignee", "assignee"},
	{"author", "author"},
}

// Reconcile implements sdk.Provider: it does the full search-based sweep of the
// user's involved pull requests and emits item.observed snapshots. The sweep is
// authoritative — it enumerates every open roled PR with no incremental cursor,
// so the engine can reap items that left it (ADR-0024). Complete is true unless
// a sole-approver probe silently failed, which would leave the identity set
// short and must suppress reaping.
func (p *Provider) Reconcile(ctx context.Context, _ sdk.PollState) (sdk.PollResult, error) {
	// Accumulate roles per PR URL across all scoped queries, then emit one
	// deduplicated item.observed carrying every role that matched.
	type agg struct {
		item  ghSearchItem
		repo  string
		roles []string
	}
	seen := map[string]*agg{}
	var order []string
	var events []sdk.Event
	var rate *int
	complete := true

	scopes := append(append([]searchScope{}, reconcileScopes...), searchScope{"user", roleSoleApprover})

	for _, sc := range scopes {
		q := fmt.Sprintf("is:pr is:open %s:%s", sc.qualifier, p.handle)
		res, r, err := p.search(ctx, q)
		if err != nil {
			return sdk.PollResult{}, err
		}
		if r != nil {
			rate = r
		}
		for _, it := range res.Items {
			if it.PullRequest == nil {
				continue
			}

			repo := repoFromSearchItem(it)

			if sc.role == roleSoleApprover {
				keep, kErr := p.keepAsSoleApprover(ctx, it, repo)
				if kErr != nil {
					// A failed probe leaves the sole-approver set incomplete; skip the
					// item but mark the sweep non-authoritative so it does not reap.
					complete = false
					continue
				}
				if !keep {
					continue
				}
			}

			a, ok := seen[it.HTMLURL]
			if !ok {
				a = &agg{item: it, repo: repo}
				seen[it.HTMLURL] = a
				order = append(order, it.HTMLURL)
			}
			a.roles = append(a.roles, sc.role)
			// Entering the review-requested set is the review-request signal.
			if sc.role == "reviewer" {
				events = append(events, sdk.Event{
					ObjectType: objectType,
					NativeID:   nativeID(a.repo, it.Number),
					EventType:  sdk.SignalReviewRequested,
					OccurredAt: parseTime(it.UpdatedAt),
					DedupeKey:  fmt.Sprintf("%s:%s:%s", sdk.SignalReviewRequested, nativeID(a.repo, it.Number), it.UpdatedAt),
					Payload:    sdk.SignalReviewRequestedPayload{},
				})
			}
		}
	}

	for _, u := range order {
		a := seen[u]
		events = append(events, p.observedFromSearch(a.item, a.repo, sortedRoles(a.roles)))
	}

	events, degraded, err := p.graphqlJoin(ctx, events)
	if err != nil {
		return sdk.PollResult{}, err
	}
	p.cacheOpenSnapshots(events)
	p.pruneClosedSnapshots(events, complete)

	return sdk.PollResult{
		Events:        events,
		RateRemaining: rate,
		Degraded:      degraded,
		Complete:      complete,
	}, nil
}

// pruneClosedSnapshots evicts cached open-item snapshots whose PR is absent from
// an authoritative Reconcile sweep — no longer an open roled PR, so its
// carried-forward enrichment would only grow the cache without ever being read.
// Only the full sweep may prune: FastPoll sees a partial, notification-driven
// slice and must not evict PRs it merely did not hear about this cycle. When the
// sweep is incomplete (a sole-approver probe failed) it is not authoritative, so
// eviction is skipped, matching the engine's reap suppression (ADR-0024). The
// kept set mirrors cacheOpenSnapshots' store criterion exactly (open
// item.observed events), so after cache+prune openSnapshots holds precisely this
// sweep's open set.
func (p *Provider) pruneClosedSnapshots(events []sdk.Event, complete bool) {
	if !complete {
		return
	}
	swept := make(map[string]struct{}, len(events))
	for _, ev := range events {
		if ev.EventType != sdk.EventItemObserved {
			continue
		}
		if pl, ok := ev.Payload.(sdk.ItemObservedPayload); ok && pl.State == stateOpen {
			swept[ev.NativeID] = struct{}{}
		}
	}
	for id := range p.openSnapshots {
		if _, ok := swept[id]; !ok {
			delete(p.openSnapshots, id)
		}
	}
}

// search runs a Search API query, following the Link header rel="next" until
// exhausted so large result sets are never silently truncated to page one.
func (p *Provider) search(ctx context.Context, q string) (*ghSearchResult, *int, error) {
	u := p.apiBase + "/search/issues?q=" + url.QueryEscape(q) + "&per_page=100"
	var res ghSearchResult
	var rate *int
	for u != "" {
		resp, err := p.do(ctx, u, nil)
		if err != nil {
			return nil, nil, err
		}
		if r := rateRemaining(resp.Header); r != nil {
			rate = r
		}
		next := nextLink(resp.Header)
		var page ghSearchResult
		if err := decodeJSON(resp, &page); err != nil {
			return nil, nil, err
		}
		res.Items = append(res.Items, page.Items...)
		u = next
	}
	return &res, rate, nil
}

// nextLink returns the rel="next" URL from an RFC 5988 Link header, or "" when
// there is no further page.
func nextLink(h http.Header) string {
	for _, link := range h.Values("Link") {
		for part := range strings.SplitSeq(link, ",") {
			segs := strings.Split(part, ";")
			if len(segs) < 2 {
				continue
			}
			ref := strings.TrimSpace(segs[0])
			if !strings.HasPrefix(ref, "<") || !strings.HasSuffix(ref, ">") {
				continue
			}
			for _, param := range segs[1:] {
				if strings.TrimSpace(param) == `rel="next"` {
					return ref[1 : len(ref)-1]
				}
			}
		}
	}
	return ""
}
