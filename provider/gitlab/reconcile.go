package gitlab

import (
	"context"
	"errors"
	"net/url"
	"time"

	"github.com/vilaca/devpit/sdk"
)

const cursorRecUpdatedAfter = "gl.rec.updated_after"

var reconcileScopes = []string{"assigned", "created"}

func (p *Provider) Reconcile(ctx context.Context, state sdk.PollState) (sdk.PollResult, error) {
	if state == nil {
		state = sdk.PollState{}
	}
	out := sdk.PollState{}
	for k, v := range state {
		out[k] = v
	}

	now := time.Now().UTC().Format(time.RFC3339)
	updatedAfter := state[cursorRecUpdatedAfter]

	seen := map[string]bool{}
	var events []sdk.Event
	var rate *int

	for _, scope := range reconcileScopes {
		u := p.apiBase + "/merge_requests?scope=" + scope + "&state=opened"
		if updatedAfter != "" {
			u += "&updated_after=" + url.QueryEscape(updatedAfter)
		}
		resp, err := p.do(ctx, "GET", u)
		if err != nil {
			var re *rateError
			if errors.As(err, &re) {
				out[cursorFastRetryAfter] = re.retryAfter
				return sdk.PollResult{Events: events, State: out}, err
			}
			return sdk.PollResult{}, err
		}
		if r := rateRemaining(resp.Header); r != nil {
			rate = r
		}
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
	}

	out[cursorRecUpdatedAfter] = now
	return sdk.PollResult{
		Events:        events,
		State:         out,
		RateRemaining: rate,
		ItemsChanged:  len(events),
	}, nil
}
