package github

import (
	"context"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/vilaca/devpit/sdk"
)

const cursorRecUpdatedAfter = "gh.rec.updated_after"

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
// user's involved pull requests and emits item.observed snapshots.
func (p *Provider) Reconcile(ctx context.Context, state sdk.PollState) (sdk.PollResult, error) {
	if state == nil {
		state = sdk.PollState{}
	}
	out := sdk.PollState{}
	maps.Copy(out, state)

	updatedFilter := ""
	if ua := state[cursorRecUpdatedAfter]; ua != "" {
		updatedFilter = " updated:>" + ua
	}

	// Accumulate roles per PR URL across the three scoped queries, then emit
	// one deduplicated item.observed carrying every role that matched.
	type agg struct {
		item  ghSearchItem
		repo  string
		roles []string
	}
	seen := map[string]*agg{}
	var order []string
	var events []sdk.Event
	var rate *int

	for _, sc := range reconcileScopes {
		q := fmt.Sprintf("is:pr is:open %s:%s%s", sc.qualifier, p.handle, updatedFilter)
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
			a, ok := seen[it.HTMLURL]
			if !ok {
				a = &agg{item: it, repo: repoFromSearchItem(it)}
				seen[it.HTMLURL] = a
				order = append(order, it.HTMLURL)
			}
			a.roles = append(a.roles, sc.role)
			// Entering the review-requested set is the review-request signal.
			if sc.role == "reviewer" {
				events = append(events, sdk.Event{
					ObjectType: objectType,
					NativeID:   nativeID(a.repo, it.Number),
					EventType:  "signal.review_requested",
					OccurredAt: parseTime(it.UpdatedAt),
					DedupeKey:  fmt.Sprintf("signal.review_requested:%s:%s", nativeID(a.repo, it.Number), it.UpdatedAt),
					Payload:    sdk.SignalReviewRequestedPayload{Direct: true},
				})
			}
		}
	}

	for _, u := range order {
		a := seen[u]
		events = append(events, p.observedFromSearch(a.item, a.repo, sortedRoles(a.roles)))
	}

	events = p.graphqlJoin(ctx, events)

	out[cursorRecUpdatedAfter] = time.Now().UTC().Format(time.RFC3339)
	return sdk.PollResult{
		Events:        events,
		State:         out,
		RateRemaining: rate,
		ItemsChanged:  len(events),
	}, nil
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
