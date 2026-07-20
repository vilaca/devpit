package gitlab

import (
	"context"
	"fmt"
	"maps"
	"net/url"
	"time"

	"github.com/vilaca/devpit/sdk"
)

const cursorFastUpdatedAfter = "gl.fast.updated_after"

// FastPoll implements sdk.Provider: it polls the pending todos feed and emits
// fast signals for the current user.
func (p *Provider) FastPoll(ctx context.Context, state sdk.PollState) (sdk.PollResult, error) {
	if state == nil {
		state = sdk.PollState{}
	}
	out := sdk.PollState{}
	maps.Copy(out, state)

	// Watermark is set to now at the end of the cycle; the cursor advance is
	// unconditional (no 304 on GitLab).
	now := time.Now().UTC().Format(time.RFC3339)

	u := p.apiBase + "/todos?state=pending"
	if ua := state[cursorFastUpdatedAfter]; ua != "" {
		u += "&updated_after=" + url.QueryEscape(ua)
	}
	resp, err := p.do(ctx, u)
	if err != nil {
		return sdk.PollResult{}, err
	}
	rate := rateRemaining(resp.Header)

	var todos []glTodo
	if err := decodeJSON(resp, &todos); err != nil {
		return sdk.PollResult{}, err
	}

	var events []sdk.Event
	for _, t := range todos {
		if t.TargetType != "MergeRequest" {
			continue
		}
		mr, err := p.fetchMR(ctx, t.Project.ID, t.Target.IID)
		if err != nil {
			return sdk.PollResult{}, err
		}
		obs := p.observedFromMR(*mr)
		events = append(events, obs)
		if sig := signalFromTodo(t, obs.NativeID); sig != nil {
			events = append(events, *sig)
		}
	}

	out[cursorFastUpdatedAfter] = now
	return sdk.PollResult{
		Events:        events,
		State:         out,
		RateRemaining: rate,
		ItemsChanged:  len(events),
	}, nil
}

func (p *Provider) fetchMR(ctx context.Context, projectID, iid int) (*glMergeRequest, error) {
	u := fmt.Sprintf("%s/projects/%d/merge_requests/%d", p.apiBase, projectID, iid)
	resp, err := p.do(ctx, u)
	if err != nil {
		return nil, err
	}
	var mr glMergeRequest
	if err := decodeJSON(resp, &mr); err != nil {
		return nil, err
	}
	return &mr, nil
}

// signalFromTodo maps a todo's action_name to a signal event, keyed on the
// native todo id (no duplicate pending todo exists, so the id is a stable
// dedupe key — docs/Event_Taxonomy_and_Storage.md).
func signalFromTodo(t glTodo, nid string) *sdk.Event {
	base := func(eventType string, payload any) *sdk.Event {
		return &sdk.Event{
			ObjectType: objectType,
			NativeID:   nid,
			EventType:  eventType,
			OccurredAt: parseTime(t.UpdatedAt),
			Actor:      t.Author.Username,
			DedupeKey:  fmt.Sprintf("%s:todo:%d", eventType, t.ID),
			Payload:    payload,
		}
	}
	switch t.ActionName {
	case "mentioned":
		return base("signal.mentioned", sdk.SignalMentionedPayload{Direct: false})
	case "directly_addressed":
		return base("signal.mentioned", sdk.SignalMentionedPayload{Direct: true})
	case "review_requested":
		return base("signal.review_requested", sdk.SignalReviewRequestedPayload{Direct: true})
	case "review_submitted":
		return base("signal.review_submitted", sdk.SignalReviewSubmittedPayload{Reviewer: t.Author.Username})
	case "assigned":
		return base("signal.assigned", sdk.SignalAssignedPayload{Assigner: t.Author.Username})
	case "build_failed":
		return base("signal.ci_failed", sdk.SignalCIFailedPayload{})
	default:
		return nil
	}
}
