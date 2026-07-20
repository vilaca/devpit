package github

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/vilaca/devpit/sdk"
)

const (
	cursorFastLastModified = "gh.fast.last_modified"
	cursorFastETag         = "gh.fast.etag"
	cursorFastRetryAfter   = "gh.fast.retry_after"
)

func (p *Provider) FastPoll(ctx context.Context, state sdk.PollState) (sdk.PollResult, error) {
	if state == nil {
		state = sdk.PollState{}
	}
	out := sdk.PollState{}
	for k, v := range state {
		out[k] = v
	}

	hdr := http.Header{}
	if lm := state[cursorFastLastModified]; lm != "" {
		hdr.Set("If-Modified-Since", lm)
	}
	if et := state[cursorFastETag]; et != "" {
		hdr.Set("If-None-Match", et)
	}

	resp, err := p.do(ctx, "GET", p.apiBase+"/notifications", hdr)
	if err != nil {
		var re *rateError
		if errors.As(err, &re) {
			out[cursorFastRetryAfter] = re.retryAfter
			return sdk.PollResult{State: out}, err
		}
		return sdk.PollResult{}, err
	}

	if resp.StatusCode == http.StatusNotModified {
		_ = resp.Body.Close()
		return sdk.PollResult{State: out}, nil
	}

	if lm := resp.Header.Get("Last-Modified"); lm != "" {
		out[cursorFastLastModified] = lm
	}
	if et := resp.Header.Get("ETag"); et != "" {
		out[cursorFastETag] = et
	}
	rate := rateRemaining(resp.Header)

	var notifs []ghNotification
	if err := decodeJSON(resp, &notifs); err != nil {
		return sdk.PollResult{}, err
	}

	var events []sdk.Event
	for _, n := range notifs {
		if n.Subject.Type != "PullRequest" {
			continue
		}
		owner, repo, number, ok := parsePullURL(n.Subject.URL)
		if !ok {
			continue
		}
		pr, err := p.fetchPull(ctx, owner, repo, number)
		if err != nil {
			var re *rateError
			if errors.As(err, &re) {
				out[cursorFastRetryAfter] = re.retryAfter
				return sdk.PollResult{Events: events, State: out}, err
			}
			return sdk.PollResult{}, err
		}
		obs := p.observedFromPull(*pr)
		events = append(events, obs)
		events = append(events, p.signalsFromNotification(n, obs.NativeID)...)
	}

	return sdk.PollResult{
		Events:        events,
		State:         out,
		RateRemaining: rate,
		ItemsChanged:  len(events),
	}, nil
}

func (p *Provider) fetchPull(ctx context.Context, owner, repo string, number int) (*ghPull, error) {
	url := fmt.Sprintf("%s/repos/%s/%s/pulls/%d", p.apiBase, owner, repo, number)
	resp, err := p.do(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	var pr ghPull
	if err := decodeJSON(resp, &pr); err != nil {
		return nil, err
	}
	return &pr, nil
}

// signalsFromNotification derives signal events from the notification reason.
// occurredAt uses the notification's updated_at so re-notification of the same
// state dedupes and a genuinely new occurrence gets a fresh key (taxonomy §5).
func (p *Provider) signalsFromNotification(n ghNotification, nid string) []sdk.Event {
	occ := parseTime(n.UpdatedAt)
	base := func(eventType string, payload any) sdk.Event {
		return sdk.Event{
			ObjectType: objectType,
			NativeID:   nid,
			EventType:  eventType,
			OccurredAt: occ,
			DedupeKey:  fmt.Sprintf("%s:%s:%s", eventType, nid, n.UpdatedAt),
			Payload:    payload,
		}
	}
	switch n.Reason {
	case "mention":
		return []sdk.Event{base("signal.mentioned", sdk.SignalMentionedPayload{Direct: true})}
	case "team_mention":
		return []sdk.Event{base("signal.mentioned", sdk.SignalMentionedPayload{Direct: false})}
	case "review_requested":
		return []sdk.Event{base("signal.review_requested", sdk.SignalReviewRequestedPayload{Direct: true})}
	case "assign":
		return []sdk.Event{base("signal.assigned", sdk.SignalAssignedPayload{})}
	case "ci_activity":
		return []sdk.Event{base("signal.ci_failed", sdk.SignalCIFailedPayload{})}
	default:
		return nil
	}
}
