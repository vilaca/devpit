package gitlab

import (
	"context"
	"errors"
	"io"
	"net/http"
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
	p, err := New(sdk.ConnectionConfig{Type: "gitlab", Token: "test-token"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	p.http.Transport = rt
	return p
}

func newTestProvider(t *testing.T, cassette, handle string) *Provider {
	t.Helper()
	rec, err := recorder.NewWithOptions(&recorder.Options{
		CassetteName:       "../../testdata/fixtures/gitlab/" + cassette,
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
		Type:    "gitlab",
		BaseURL: "https://gitlab.com",
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
			if e.NativeID != "acme/api!7" {
				t.Errorf("native id = %q", e.NativeID)
			}
			pl, ok := e.Payload.(sdk.ItemObservedPayload)
			if !ok {
				t.Fatalf("item.observed payload has unexpected type %T", e.Payload)
			}
			if pl.Gate != "blocked" {
				t.Errorf("gate = %q, want blocked", pl.Gate)
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
	if res.State[cursorFastUpdatedAfter] == "" {
		t.Errorf("updated_after cursor not set")
	}
}

func TestReconcileDedup(t *testing.T) {
	p := newTestProvider(t, "reconcile", "octocat")
	res, err := p.Reconcile(context.Background(), nil)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	count := map[string]int{}
	for _, e := range res.Events {
		if e.EventType == "item.observed" {
			count[e.NativeID]++
		}
	}
	if count["acme/api!7"] != 1 {
		t.Errorf("acme/api!7 observed %d times, want 1 (deduped)", count["acme/api!7"])
	}
	if res.State[cursorRecUpdatedAfter] == "" {
		t.Errorf("reconcile cursor not set")
	}
}

// TestReconcilePagination verifies Reconcile follows the X-Next-Page cursor on
// /merge_requests and returns MRs from every page.
func TestReconcilePagination(t *testing.T) {
	p := newTestProvider(t, "reconcile_paginated", "octocat")
	res, err := p.Reconcile(context.Background(), nil)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	seen := map[string]bool{}
	for _, e := range res.Events {
		if e.EventType == "item.observed" {
			seen[e.NativeID] = true
		}
	}
	if !seen["acme/api!7"] || !seen["acme/api!8"] {
		t.Fatalf("observed = %v, want both !7 (page 1) and !8 (page 2)", seen)
	}
}

// TestFastPollPagination verifies FastPoll follows the X-Next-Page cursor on
// /todos and processes todos from every page.
func TestFastPollPagination(t *testing.T) {
	p := newTestProvider(t, "fastpoll_paginated", "octocat")
	res, err := p.FastPoll(context.Background(), nil)
	if err != nil {
		t.Fatalf("FastPoll: %v", err)
	}

	seen := map[string]bool{}
	for _, e := range res.Events {
		if e.EventType == "item.observed" {
			seen[e.NativeID] = true
		}
	}
	if !seen["acme/api!7"] || !seen["acme/api!8"] {
		t.Fatalf("observed = %v, want both !7 (page 1) and !8 (page 2)", seen)
	}
}

func TestRegistered(t *testing.T) {
	if _, ok := sdk.Registry["gitlab"]; !ok {
		t.Fatal("gitlab not registered")
	}
}

func makeMR(detailedStatus string) glMergeRequest {
	mr := glMergeRequest{
		IID:                 7,
		ProjectID:           1,
		Title:               "T",
		WebURL:              "https://gitlab.com/acme/api/-/merge_requests/7",
		State:               "opened",
		DetailedMergeStatus: detailedStatus,
		UpdatedAt:           "2026-07-10T00:00:00Z",
		Author:              glUser{Username: "octocat"},
	}
	mr.References.Full = "acme/api!7"
	return mr
}

func TestDoStatusClassification(t *testing.T) {
	cases := []struct {
		name       string
		status     int
		wantErr    error
		wantStatus int // asserted when the error is a *sdk.StatusError
	}{
		{"ok", http.StatusOK, nil, 0},
		{"unauthorized", http.StatusUnauthorized, sdk.ErrUnauthorized, 0},
		{"forbidden", http.StatusForbidden, sdk.ErrRateLimited, 0},
		{"too many requests", http.StatusTooManyRequests, sdk.ErrRateLimited, 0},
		{"server error", http.StatusInternalServerError, sdk.ErrServer, 500},
		{"service unavailable", http.StatusServiceUnavailable, sdk.ErrServer, 503},
		{"unexpected", http.StatusTeapot, sdk.ErrUnexpected, 418},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := newStubProvider(t, stubRT{status: c.status, body: "[]"})
			resp, err := p.do(context.Background(), "https://gitlab.com/api/v4/x")
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

// TestRateLimitDelay verifies a 429 carries the Retry-After hint through the
// *sdk.RateLimitError the engine reads for backoff.
func TestRateLimitDelay(t *testing.T) {
	h := http.Header{}
	h.Set("Retry-After", "45")
	p := newStubProvider(t, stubRT{status: http.StatusTooManyRequests, header: h})
	_, err := p.do(context.Background(), "https://gitlab.com/api/v4/x")
	var rle *sdk.RateLimitError
	if !errors.As(err, &rle) {
		t.Fatalf("err = %v, want *sdk.RateLimitError", err)
	}
	if rle.RetryAfter != 45*time.Second {
		t.Errorf("RetryAfter = %v, want 45s", rle.RetryAfter)
	}
}

func TestParseRateDelay(t *testing.T) {
	cases := []struct {
		name string
		set  bool
		val  string
		want time.Duration
	}{
		{"present", true, "45", 45 * time.Second},
		{"absent", false, "", 0},
		{"zero", true, "0", 0},
		{"malformed", true, "abc", 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			resp := &http.Response{Header: http.Header{}}
			if c.set {
				resp.Header.Set("Retry-After", c.val)
			}
			if got := parseRateDelay(resp); got != c.want {
				t.Errorf("d = %v, want %v", got, c.want)
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
		{"present", true, "1998", new(1998)},
		{"absent", false, "", nil},
		{"malformed", true, "abc", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := http.Header{}
			if c.set {
				h.Set("Ratelimit-Remaining", c.val)
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
		status       string
		wantGate     string
		wantFailing  bool
		wantConflict bool
		wantRebase   bool
	}{
		{"ci_must_pass", "blocked", true, false, false},
		{"conflict", "blocked", false, true, false},
		{"need_rebase", "blocked", false, false, true},
		{"mergeable", "ready", false, false, false},
		{"not_approved", "blocked", false, false, false},
	}
	for _, c := range cases {
		t.Run(c.status, func(t *testing.T) {
			pl, ok := p.observedFromMR(makeMR(c.status)).Payload.(sdk.ItemObservedPayload)
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
