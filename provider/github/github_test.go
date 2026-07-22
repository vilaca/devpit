package github

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/vilaca/devpit/sdk"
	"gopkg.in/dnaeon/go-vcr.v3/recorder"
)

// stubRT is a synthetic http.RoundTripper returning a fixed status, headers and
// body so do()'s status classification can be exercised without a live server.
type stubRT struct {
	status int
	header http.Header
	body   string
}

func (rt stubRT) RoundTrip(_ *http.Request) (*http.Response, error) {
	h := rt.header
	if h == nil {
		h = http.Header{}
	}
	return &http.Response{
		StatusCode: rt.status,
		Header:     h,
		Body:       io.NopCloser(strings.NewReader(rt.body)),
	}, nil
}

func newStubProvider(t *testing.T, rt stubRT) *Provider {
	t.Helper()
	p, err := New(sdk.ConnectionConfig{Type: "github", Token: "test-token"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	p.http.Transport = rt
	return p
}

// newTestProvider wires the provider's HTTP client to a replay-only VCR
// recorder backed by the named cassette. Replayable interactions are enabled
// so the reconcile sweep can re-hit the same recorded endpoints.
func newTestProvider(t *testing.T, cassette, handle string) *Provider {
	t.Helper()
	rec, err := recorder.NewWithOptions(&recorder.Options{
		CassetteName:       "../../testdata/fixtures/github/" + cassette,
		Mode:               recorder.ModeReplayOnly,
		SkipRequestLatency: true,
	})
	if err != nil {
		t.Fatalf("recorder: %v", err)
	}
	rec.SetReplayableInteractions(true)
	t.Cleanup(func() { _ = rec.Stop() })

	p, err := New(sdk.ConnectionConfig{
		ID:      "conn1",
		Type:    "github",
		BaseURL: "https://github.com",
		Token:   "test-token",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	p.http.Transport = rec
	p.handle = handle
	return p
}

func TestResolveIdentity(t *testing.T) {
	p := newTestProvider(t, "identity", "")
	id, err := p.ResolveIdentity(context.Background())
	if err != nil {
		t.Fatalf("ResolveIdentity: %v", err)
	}
	if id.Handle != "octocat" {
		t.Fatalf("handle = %q, want octocat", id.Handle)
	}
}

func TestResolveIdentityUnauthorized(t *testing.T) {
	p := newTestProvider(t, "identity_401", "")
	_, err := p.ResolveIdentity(context.Background())
	if !errors.Is(err, sdk.ErrUnauthorized) {
		t.Fatalf("err = %v, want ErrUnauthorized", err)
	}
}

func TestResolveIdentityNoLogin(t *testing.T) {
	p := newTestProvider(t, "identity_nologin", "")
	_, err := p.ResolveIdentity(context.Background())
	if !errors.Is(err, sdk.ErrManualIdentityRequired) {
		t.Fatalf("err = %v, want ErrManualIdentityRequired", err)
	}
}

func TestFastPoll(t *testing.T) {
	p := newTestProvider(t, "fastpoll", "octocat")
	res, err := p.FastPoll(context.Background(), nil)
	if err != nil {
		t.Fatalf("FastPoll: %v", err)
	}

	var observed, reviewReq int
	for _, e := range res.Events {
		switch e.EventType {
		case "item.observed":
			observed++
			pl, ok := e.Payload.(sdk.ItemObservedPayload)
			if !ok {
				t.Fatalf("item.observed payload has unexpected type %T", e.Payload)
			}
			if pl.Repo != "acme/api" {
				t.Errorf("repo = %q", pl.Repo)
			}
			if e.NativeID != "acme/api#42" {
				t.Errorf("native id = %q", e.NativeID)
			}
		case "signal.review_requested":
			reviewReq++
		}
	}
	if observed != 1 {
		t.Errorf("observed = %d, want 1", observed)
	}
	if reviewReq != 1 {
		t.Errorf("review_requested = %d, want 1", reviewReq)
	}
	if res.State[cursorFastETag] == "" {
		t.Errorf("etag cursor not set")
	}
	if res.State[cursorFastLastModified] == "" {
		t.Errorf("last-modified cursor not set")
	}
}

func TestFastPollNotModified(t *testing.T) {
	p := newTestProvider(t, "fastpoll_304", "octocat")
	state := sdk.PollState{
		cursorFastLastModified: "Wed, 08 Jul 2026 09:00:00 GMT",
		cursorFastETag:         `W/"abc"`,
	}
	res, err := p.FastPoll(context.Background(), state)
	if err != nil {
		t.Fatalf("FastPoll: %v", err)
	}
	if len(res.Events) != 0 {
		t.Fatalf("events = %d, want 0 on 304", len(res.Events))
	}
	if res.State[cursorFastETag] != `W/"abc"` {
		t.Errorf("etag cursor lost on 304")
	}
}

func TestReconcileDedup(t *testing.T) {
	p := newTestProvider(t, "reconcile", "octocat")
	res, err := p.Reconcile(context.Background(), nil)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	// PR #42 appears in both the review-requested and author result sets; it
	// must yield a single item.observed carrying both roles.
	observedByID := map[string]sdk.ItemObservedPayload{}
	for _, e := range res.Events {
		if e.EventType == "item.observed" {
			if _, dup := observedByID[e.NativeID]; dup {
				t.Fatalf("duplicate item.observed for %s", e.NativeID)
			}
			pl, ok := e.Payload.(sdk.ItemObservedPayload)
			if !ok {
				t.Fatalf("item.observed payload has unexpected type %T", e.Payload)
			}
			observedByID[e.NativeID] = pl
		}
	}
	pl, ok := observedByID["acme/api#42"]
	if !ok {
		t.Fatalf("missing observed for acme/api#42; got %v", observedByID)
	}
	if len(pl.MyRoles) != 2 {
		t.Errorf("roles = %v, want reviewer+author", pl.MyRoles)
	}
	if res.State[cursorRecUpdatedAfter] == "" {
		t.Errorf("reconcile cursor not set")
	}
}

// TestSearchPagination verifies search() follows the Link header rel="next"
// and returns items from every page, not just the first.
func TestSearchPagination(t *testing.T) {
	p := newTestProvider(t, "search_paginated", "octocat")
	res, rate, err := p.search(context.Background(), "test")
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(res.Items) != 2 {
		t.Fatalf("items = %d, want 2 (both pages)", len(res.Items))
	}
	if res.Items[0].Number != 1 || res.Items[1].Number != 2 {
		t.Errorf("items = %v, want #1 then #2", res.Items)
	}
	// Rate is taken from the last page fetched.
	if rate == nil || *rate != 27 {
		t.Errorf("rate = %v, want 27 (from last page)", rate)
	}
}

func TestRegistered(t *testing.T) {
	if _, ok := sdk.Registry["github"]; !ok {
		t.Fatal("github not registered")
	}
}

func makePR(mergeableState string) ghPull {
	pr := ghPull{
		Number:         1,
		Title:          "T",
		HTMLURL:        "https://github.com/acme/api/pull/1",
		State:          "open",
		User:           ghUser{Login: "octocat"},
		MergeableState: mergeableState,
		UpdatedAt:      "2026-07-10T00:00:00Z",
	}
	pr.Base.Repo.FullName = "acme/api"
	return pr
}

func TestDoStatusClassification(t *testing.T) {
	cases := []struct {
		name       string
		status     int
		wantErr    error
		wantStatus int // asserted when the error is a *sdk.StatusError
	}{
		{"ok", http.StatusOK, nil, 0},
		{"not modified", http.StatusNotModified, nil, 0},
		{"unauthorized", http.StatusUnauthorized, sdk.ErrUnauthorized, 0},
		{"forbidden", http.StatusForbidden, sdk.ErrRateLimited, 0},
		{"too many requests", http.StatusTooManyRequests, sdk.ErrRateLimited, 0},
		{"server error", http.StatusInternalServerError, sdk.ErrServer, 500},
		{"service unavailable", http.StatusServiceUnavailable, sdk.ErrServer, 503},
		{"unexpected", http.StatusTeapot, sdk.ErrUnexpected, 418},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := newStubProvider(t, stubRT{status: c.status, body: "{}"})
			resp, err := p.do(context.Background(), "https://api.github.com/x", nil)
			if resp != nil {
				_ = resp.Body.Close()
			}
			if c.wantErr == nil {
				if err != nil {
					t.Fatalf("err = %v, want nil", err)
				}
				if resp == nil || resp.StatusCode != c.status {
					t.Fatalf("resp status = %v, want %d", resp, c.status)
				}
				return
			}
			if !errors.Is(err, c.wantErr) {
				t.Fatalf("err = %v, want %v", err, c.wantErr)
			}
			if c.wantStatus != 0 {
				var se *sdk.StatusError
				if !errors.As(err, &se) {
					t.Fatalf("err = %v, want *sdk.StatusError", err)
				}
				if se.Status != c.wantStatus {
					t.Errorf("status = %d, want %d", se.Status, c.wantStatus)
				}
			}
		})
	}
}

func TestParseRateDelay(t *testing.T) {
	future := time.Now().Add(time.Hour).Unix()
	near := time.Now().Add(30 * time.Second).Unix()
	// wantMin/wantMax bound the result; reset uses time.Until so its exact value
	// drifts a few ms between the header value and the call.
	cases := []struct {
		name       string
		retryAfter string
		reset      string
		wantMin    time.Duration
		wantMax    time.Duration
	}{
		{"only retry-after", "45", "", 45 * time.Second, 45 * time.Second},
		{"only reset", "", strconv.FormatInt(future, 10), 55 * time.Minute, time.Hour},
		{"both reset larger", "10", strconv.FormatInt(future, 10), 55 * time.Minute, time.Hour},
		{"both retry-after larger", "7200", strconv.FormatInt(near, 10), 7200 * time.Second, 7200 * time.Second},
		{"neither", "", "", 0, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			resp := &http.Response{Header: http.Header{}}
			if c.retryAfter != "" {
				resp.Header.Set("Retry-After", c.retryAfter)
			}
			if c.reset != "" {
				resp.Header.Set("X-Ratelimit-Reset", c.reset)
			}
			if d := parseRateDelay(resp); d < c.wantMin || d > c.wantMax {
				t.Errorf("d = %v, want [%v, %v]", d, c.wantMin, c.wantMax)
			}
		})
	}
}

func TestRateRemaining(t *testing.T) {
	cases := []struct {
		name string
		set  bool
		val  string
		want *int
	}{
		{"present", true, "29", new(29)},
		{"absent", false, "", nil},
		{"malformed", true, "abc", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := http.Header{}
			if c.set {
				h.Set("X-Ratelimit-Remaining", c.val)
			}
			got := rateRemaining(h)
			switch {
			case c.want == nil && got != nil:
				t.Errorf("got %d, want nil", *got)
			case c.want != nil && (got == nil || *got != *c.want):
				t.Errorf("got %v, want %d", got, *c.want)
			}
		})
	}
}

func TestNormalizeMarkers(t *testing.T) {
	p := &Provider{handle: "octocat"}
	cases := []struct {
		mergeableState string
		wantGate       string
		wantFailing    bool
		wantConflict   bool
		wantRebase     bool
	}{
		{"unstable", "ready", true, false, false},
		{"dirty", "blocked", false, true, false},
		{"behind", "blocked", false, false, true},
		{"clean", "ready", false, false, false},
		{"blocked", "blocked", false, false, false},
		{"", "unknown", false, false, false},
	}
	for _, c := range cases {
		t.Run(c.mergeableState, func(t *testing.T) {
			pl, ok := p.observedFromPull(makePR(c.mergeableState)).Payload.(sdk.ItemObservedPayload)
			if !ok {
				t.Fatal("payload type assertion failed")
			}
			if pl.Gate != c.wantGate {
				t.Errorf("gate = %q, want %q", pl.Gate, c.wantGate)
			}
			if pl.FailingChecks != c.wantFailing {
				t.Errorf("failing_checks = %v, want %v", pl.FailingChecks, c.wantFailing)
			}
			if pl.MergeConflict != c.wantConflict {
				t.Errorf("merge_conflict = %v, want %v", pl.MergeConflict, c.wantConflict)
			}
			if pl.NeedsRebase != c.wantRebase {
				t.Errorf("needs_rebase = %v, want %v", pl.NeedsRebase, c.wantRebase)
			}
		})
	}
}

func TestGraphQLJoinReviewRequired(t *testing.T) {
	p := newTestProvider(t, "graphql_join_review_required", "octocat")
	res, err := p.FastPoll(context.Background(), nil)
	if err != nil {
		t.Fatalf("FastPoll: %v", err)
	}
	for _, e := range res.Events {
		if e.EventType == "item.observed" && e.NativeID == "acme/api#42" {
			pl, ok := e.Payload.(sdk.ItemObservedPayload)
			if !ok {
				t.Fatal("payload type assertion failed")
			}
			if !pl.NeedsApproval {
				t.Error("needs_approval should be true for REVIEW_REQUIRED blocked non-draft PR")
			}
			return
		}
	}
	t.Fatal("missing item.observed for acme/api#42")
}

func TestGraphQLJoinDegraded(t *testing.T) {
	p := newTestProvider(t, "graphql_join_degraded", "octocat")
	res, err := p.FastPoll(context.Background(), nil)
	if err != nil {
		t.Fatalf("FastPoll: %v", err)
	}
	for _, e := range res.Events {
		if e.EventType == "item.observed" && e.NativeID == "acme/api#42" {
			pl, ok := e.Payload.(sdk.ItemObservedPayload)
			if !ok {
				t.Fatal("payload type assertion failed")
			}
			if pl.NeedsApproval {
				t.Error("needs_approval should be false when GraphQL is degraded")
			}
			return
		}
	}
	t.Fatal("missing item.observed for acme/api#42")
}

// TestDirtyPRIsBlockedWithConflict verifies dirty → blocked + merge_conflict, not failing_checks.
func TestDirtyPRIsBlockedWithConflict(t *testing.T) {
	p := &Provider{handle: "octocat"}
	pl, ok := p.observedFromPull(makePR("dirty")).Payload.(sdk.ItemObservedPayload)
	if !ok {
		t.Fatal("payload type assertion failed")
	}
	if pl.Gate != "blocked" {
		t.Errorf("gate = %q, want blocked", pl.Gate)
	}
	if !pl.MergeConflict {
		t.Error("merge_conflict should be true for dirty")
	}
	if pl.FailingChecks {
		t.Error("failing_checks should be false for dirty (not unstable)")
	}
}
