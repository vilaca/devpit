package github

import (
	"context"
	"errors"
	"fmt"
	"net/url"
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

func (p *Provider) Reconcile(ctx context.Context, state sdk.PollState) (sdk.PollResult, error) {
	if state == nil {
		state = sdk.PollState{}
	}
	out := sdk.PollState{}
	for k, v := range state {
		out[k] = v
	}

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
			var re *rateError
			if errors.As(err, &re) {
				out[cursorFastRetryAfter] = re.retryAfter
				return sdk.PollResult{Events: events, State: out}, err
			}
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

	out[cursorRecUpdatedAfter] = time.Now().UTC().Format(time.RFC3339)
	return sdk.PollResult{
		Events:        events,
		State:         out,
		RateRemaining: rate,
		ItemsChanged:  len(events),
	}, nil
}

func (p *Provider) search(ctx context.Context, q string) (*ghSearchResult, *int, error) {
	u := p.apiBase + "/search/issues?q=" + url.QueryEscape(q)
	resp, err := p.do(ctx, "GET", u, nil)
	if err != nil {
		return nil, nil, err
	}
	rate := rateRemaining(resp.Header)
	var res ghSearchResult
	if err := decodeJSON(resp, &res); err != nil {
		return nil, nil, err
	}
	return &res, rate, nil
}
