package gitlab

import (
	"context"
	"maps"
	"net/url"
	"time"

	"github.com/vilaca/devpit/sdk"
)

const cursorRecUpdatedAfter = "gl.rec.updated_after"

// GitLab's global /merge_requests endpoint only accepts these scope values;
// "assigned"/"created" return 400 "scope does not have a valid value".
var reconcileScopes = []string{"assigned_to_me", "created_by_me"}

// Reconcile implements sdk.Provider: it sweeps the user's involved merge
// requests across the reconcile scopes and emits item.observed snapshots.
func (p *Provider) Reconcile(ctx context.Context, state sdk.PollState) (sdk.PollResult, error) {
	if state == nil {
		state = sdk.PollState{}
	}
	out := sdk.PollState{}
	maps.Copy(out, state)

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
		resp, err := p.do(ctx, u)
		if err != nil {
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
