package gitlab

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

// testBaseURL is the GitLab base URL shared across test providers.
const testBaseURL = "https://gitlab.com"

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
		BaseURL: testBaseURL,
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
	// The reviewer sweep (scope=all&reviewer_username=…) must be exercised: !9 is
	// returned only by that query, so its presence proves reviewer MRs the user
	// did not author are reconciled — the gap that stranded stale reviewer MRs.
	if count["acme/api!9"] != 1 {
		t.Errorf("acme/api!9 (reviewer-only) observed %d times, want 1", count["acme/api!9"])
	}
	for _, q := range p.reconcileQueries() {
		if res.State[cursorRecQuery(q)] == "" {
			t.Errorf("reconcile cursor for %q not set", q)
		}
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
	t := true
	mr := glMergeRequest{
		IID:                         7,
		ProjectID:                   1,
		Title:                       "T",
		WebURL:                      "https://gitlab.com/acme/api/-/merge_requests/7",
		State:                       "opened",
		DetailedMergeStatus:         detailedStatus,
		HasConflicts:                false,
		BlockingDiscussionsResolved: &t,
		UpdatedAt:                   "2026-07-10T00:00:00Z",
		Author:                      glUser{Username: "octocat"},
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
	// FailingChecks (ci_must_pass), NeedsRebase (need_rebase), and NeedsApproval (not_approved) are set
	// from detailed_merge_status by observedFromMR as a REST fallback; the GraphQL join refines them with
	// the authoritative headPipeline.status / shouldBeRebased / approved fields when available.
	// MergeConflict comes from has_conflicts REST field; the makeMR helper defaults it to false.
	// UnresolvedDiscussions uses blocking_discussions_resolved (*bool); makeMR defaults to true (resolved).
	cases := []struct {
		status           string
		wantGate         string
		wantFailing      bool
		wantConflict     bool
		wantRebase       bool
		wantApproval     bool
		wantUnresolved   bool
		wantPolicyDenied bool
	}{
		{"ci_must_pass", "blocked", true, false, false, false, false, false},
		{"conflict", "blocked", false, false, false, false, false, false},
		{"need_rebase", "blocked", false, false, true, false, false, false},
		{"mergeable", "ready", false, false, false, false, false, false},
		{"not_approved", "blocked", false, false, false, true, false, false},
		{"policies_denied", "blocked", false, false, false, false, false, true},
		{"security_policy_violations", "blocked", false, false, false, false, false, true},
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
			if pl.NeedsApproval != c.wantApproval {
				t.Errorf("needs_approval = %v, want %v", pl.NeedsApproval, c.wantApproval)
			}
			if pl.UnresolvedDiscussions != c.wantUnresolved {
				t.Errorf("unresolved_discussions = %v, want %v", pl.UnresolvedDiscussions, c.wantUnresolved)
			}
			if pl.PolicyDenied != c.wantPolicyDenied {
				t.Errorf("policy_denied = %v, want %v", pl.PolicyDenied, c.wantPolicyDenied)
			}
		})
	}
}

// TestAutoMergeArmedFromREST verifies AutoMergeArmed is read from the REST
// merge_when_pipeline_succeeds field on the list payload (no GraphQL needed).
func TestAutoMergeArmedFromREST(t *testing.T) {
	p := &Provider{handle: "octocat"}

	armed := makeMR("mergeable")
	armed.MergeWhenPipelineSucceeds = true
	pl, ok := p.observedFromMR(armed).Payload.(sdk.ItemObservedPayload)
	if !ok {
		t.Fatal("payload type assertion failed")
	}
	if !pl.AutoMergeArmed {
		t.Error("auto_merge_armed should be true when merge_when_pipeline_succeeds is set")
	}

	plain, ok := p.observedFromMR(makeMR("mergeable")).Payload.(sdk.ItemObservedPayload)
	if !ok {
		t.Fatal("payload type assertion failed")
	}
	if plain.AutoMergeArmed {
		t.Error("auto_merge_armed should be false when merge_when_pipeline_succeeds is absent")
	}
}

func TestIsPipelineRunning(t *testing.T) {
	cases := []struct {
		status string
		want   bool
	}{
		{"RUNNING", true},
		{"PENDING", true},
		{"CREATED", true},
		{"WAITING_FOR_RESOURCE", true},
		{"PREPARING", true},
		{"SCHEDULED", true},
		{"SUCCESS", false},
		{"FAILED", false},
		{"CANCELED", false},
		{"SKIPPED", false},
		{"MANUAL", false},
	}
	for _, c := range cases {
		t.Run(c.status, func(t *testing.T) {
			if got := isPipelineRunning(&glPipeline{Status: c.status}); got != c.want {
				t.Errorf("isPipelineRunning(%q) = %v, want %v", c.status, got, c.want)
			}
		})
	}
	if isPipelineRunning(nil) {
		t.Error("isPipelineRunning(nil) should be false")
	}
}

// TestGraphQLJoinChecksRunning verifies a RUNNING headPipeline sets ChecksRunning
// true and FailingChecks false for the same pipeline (a running pipeline is not red).
// TestApplyGraphQLMyReviewState verifies MyReviewState is set to "approved" only
// when the authenticated handle appears in approvedBy, and never on drafts.
func TestApplyGraphQLMyReviewState(t *testing.T) {
	var mr glGraphQLMR
	if err := json.Unmarshal([]byte(
		`{"approved":true,"approvedBy":{"count":2,"nodes":[{"username":"octocat"},{"username":"other"}]}}`,
	), &mr); err != nil {
		t.Fatal(err)
	}

	if pl := applyGraphQL(sdk.ItemObservedPayload{}, mr, "octocat"); pl.MyReviewState != reviewStateApproved {
		t.Errorf("MyReviewState = %q, want approved when handle is an approver", pl.MyReviewState)
	}
	if pl := applyGraphQL(sdk.ItemObservedPayload{}, mr, "stranger"); pl.MyReviewState != "" {
		t.Errorf("MyReviewState = %q, want empty for a non-approver", pl.MyReviewState)
	}
	if pl := applyGraphQL(sdk.ItemObservedPayload{Draft: true}, mr, "octocat"); pl.MyReviewState != "" {
		t.Errorf("MyReviewState = %q, want empty on a draft (suppressed)", pl.MyReviewState)
	}
}

func TestGraphQLJoinChecksRunning(t *testing.T) {
	p := newTestProvider(t, "graphql_join_checks_running", "octocat")
	res, err := p.FastPoll(context.Background(), nil)
	if err != nil {
		t.Fatalf("FastPoll: %v", err)
	}
	for _, e := range res.Events {
		if e.EventType != "item.observed" || e.NativeID != "acme/api!7" {
			continue
		}
		pl, ok := e.Payload.(sdk.ItemObservedPayload)
		if !ok {
			t.Fatal("payload type assertion failed")
		}
		if !pl.ChecksRunning {
			t.Error("checks_running should be true: headPipeline status RUNNING")
		}
		if pl.FailingChecks {
			t.Error("failing_checks should be false: a running pipeline is not red")
		}
		return
	}
	t.Fatal("missing item.observed for acme/api!7")
}

func TestGraphQLJoinNeedsApproval(t *testing.T) {
	p := newTestProvider(t, "graphql_join_needs_approval", "octocat")
	res, err := p.Reconcile(context.Background(), nil)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	for _, e := range res.Events {
		if e.EventType == "item.observed" && e.NativeID == "acme/api!7" {
			pl, ok := e.Payload.(sdk.ItemObservedPayload)
			if !ok {
				t.Fatal("payload type assertion failed")
			}
			if !pl.NeedsApproval {
				t.Error("needs_approval should be true for not-approved non-draft MR")
			}
			return
		}
	}
	t.Fatal("missing item.observed for acme/api!7")
}

func TestGraphQLJoinMultiReason(t *testing.T) {
	p := newTestProvider(t, "graphql_join_multi_reason", "octocat")
	res, err := p.FastPoll(context.Background(), nil)
	if err != nil {
		t.Fatalf("FastPoll: %v", err)
	}
	for _, e := range res.Events {
		if e.EventType != "item.observed" || e.NativeID != "acme/api!7" {
			continue
		}
		pl, ok := e.Payload.(sdk.ItemObservedPayload)
		if !ok {
			t.Fatal("payload type assertion failed")
		}
		if !pl.MergeConflict {
			t.Error("merge_conflict should be true (has_conflicts=true)")
		}
		if !pl.UnresolvedDiscussions {
			t.Error("unresolved_discussions should be true (blocking_discussions_resolved=false)")
		}
		if !pl.NeedsApproval {
			t.Error("needs_approval should be true (approved=false)")
		}
		if !pl.FailingChecks {
			t.Error("failing_checks should be true (pipeline FAILED)")
		}
		return
	}
	t.Fatal("missing item.observed for acme/api!7")
}

// TestFastPollOpenSetRefresh verifies that fast_poll refreshes the three GraphQL
// booleans for open items not covered by a todo this cycle (anti-clobber: REST
// fields on the cached snapshot must survive the merge unchanged).
func TestFastPollOpenSetRefresh(t *testing.T) {
	p := newTestProvider(t, "fastpoll_open_set_refresh", "octocat")

	// Seed the cache as reconcile would have: full payload from last sweep.
	// MergeConflict=true is a REST field; it must survive the GraphQL merge.
	// FailingChecks=false will be overwritten to true by the cassette response.
	p.openSnapshots["acme/api!7"] = sdk.ItemObservedPayload{
		Title:         "cached MR",
		URL:           "https://gitlab.com/acme/api/-/merge_requests/7",
		Repo:          "acme/api",
		State:         stateOpen,
		Draft:         false,
		Author:        "jdoe",
		MyRoles:       []string{"reviewer"},
		Gate:          gateBlocked,
		GateDetail:    "ci_must_pass",
		FailingChecks: false,
		MergeConflict: true, // REST field — must not be clobbered by GraphQL merge
		NeedsRebase:   false,
		NeedsApproval: false,
	}

	res, err := p.FastPoll(context.Background(), nil)
	if err != nil {
		t.Fatalf("FastPoll: %v", err)
	}

	var found bool
	for _, e := range res.Events {
		if e.EventType != "item.observed" || e.NativeID != "acme/api!7" {
			continue
		}
		found = true
		pl, ok := e.Payload.(sdk.ItemObservedPayload)
		if !ok {
			t.Fatal("payload type assertion failed")
		}
		// GraphQL field updated by cassette (headPipeline=FAILED)
		if !pl.FailingChecks {
			t.Error("failing_checks should be true: pipeline FAILED in GraphQL response")
		}
		// REST field must survive the merge (anti-clobber guarantee)
		if !pl.MergeConflict {
			t.Error("merge_conflict should remain true: REST field must not be clobbered by GraphQL merge")
		}
		if pl.Title != "cached MR" {
			t.Errorf("title = %q, want %q: REST field must survive", pl.Title, "cached MR")
		}
	}
	if !found {
		t.Fatal("missing item.observed event for acme/api!7 from open-set refresh")
	}
}

// TestFastPollOpenSetRefreshDegraded verifies that a GraphQL error on the
// open-set refresh path is logged and skipped — the cycle succeeds and no
// open-set events are emitted.
func TestFastPollOpenSetRefreshDegraded(t *testing.T) {
	p := newTestProvider(t, "fastpoll_open_set_refresh_degraded", "octocat")
	p.openSnapshots["acme/api!7"] = sdk.ItemObservedPayload{
		Title:  "cached MR",
		State:  stateOpen,
		Author: "jdoe",
	}

	res, err := p.FastPoll(context.Background(), nil)
	if err != nil {
		t.Fatalf("FastPoll must succeed despite GraphQL error: %v", err)
	}
	for _, e := range res.Events {
		if e.EventType == "item.observed" && e.NativeID == "acme/api!7" {
			t.Error("open-set refresh should not emit events when GraphQL is degraded")
		}
	}
}

// TestFastPollOpenSetRefreshEmptyCache verifies no panic and no open-set refresh
// when the cache is empty (startup state, before the first reconcile).
func TestFastPollOpenSetRefreshEmptyCache(t *testing.T) {
	p := newStubProvider(t, stubRT{status: 200, body: "[]"})
	if _, err := p.FastPoll(context.Background(), nil); err != nil {
		t.Fatalf("FastPoll with empty cache: %v", err)
	}
}

// TestReconcilePopulatesOpenSnapshots verifies that Reconcile populates the
// open-set cache so a subsequent FastPoll open-set refresh has data to work with.
func TestReconcilePopulatesOpenSnapshots(t *testing.T) {
	p := newTestProvider(t, "reconcile", "octocat")
	if _, err := p.Reconcile(context.Background(), nil); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if _, ok := p.openSnapshots["acme/api!7"]; !ok {
		t.Error("openSnapshots should contain acme/api!7 after Reconcile")
	}
}

func TestGraphQLJoinGitLabDegraded(t *testing.T) {
	p := newTestProvider(t, "graphql_join_degraded", "octocat")
	res, err := p.Reconcile(context.Background(), nil)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	for _, e := range res.Events {
		if e.EventType == "item.observed" && e.NativeID == "acme/api!7" {
			pl, ok := e.Payload.(sdk.ItemObservedPayload)
			if !ok {
				t.Fatal("payload type assertion failed")
			}
			if !pl.NeedsApproval {
				t.Error("needs_approval should be true: REST gate_detail=not_approved is the fallback when GraphQL is degraded")
			}
			return
		}
	}
	t.Fatal("missing item.observed for acme/api!7")
}

// complexityCeilingRT is a fake http.RoundTripper that rejects GraphQL queries
// containing more than maxItems MR aliases with a GitLab-style complexity error,
// and returns minimal valid data otherwise.
type complexityCeilingRT struct {
	maxItems int
}

func (rt *complexityCeilingRT) RoundTrip(req *http.Request) (*http.Response, error) {
	if !strings.Contains(req.URL.Path, "graphql") {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{},
			Body:       io.NopCloser(strings.NewReader("[]")),
		}, nil
	}

	body, _ := io.ReadAll(req.Body)
	_ = req.Body.Close()
	var envelope struct {
		Query string `json:"query"`
	}
	_ = json.Unmarshal(body, &envelope)

	// Each MR in the batch produces one "project(" token in the query.
	count := strings.Count(envelope.Query, "project(")

	if count > rt.maxItems {
		resp := `{"data":null,"errors":[{"message":"Query has complexity of 420 which exceeds max complexity of 250"}]}`
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(resp)),
		}, nil
	}

	// Return a valid response: each alias gets one approved MR entry with one approver.
	data := make(map[string]any, count)
	for i := range count {
		data[fmt.Sprintf("a%d", i)] = map[string]any{
			"mergeRequest": map[string]any{
				"approved":                 true,
				"shouldBeRebased":          false,
				"divergedFromTargetBranch": false,
				"headPipeline":             nil,
				"approvedBy":               map[string]any{"count": 1, "nodes": []any{map[string]any{"username": "octocat"}}},
			},
		}
	}
	respBody, _ := json.Marshal(map[string]any{"data": data}) //nolint:errchkjson // fixed JSON-safe test map
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(bytes.NewReader(respBody)),
	}, nil
}

// TestGraphQLBatchingUnderCeiling verifies that graphqlJoin never sends a batch
// larger than graphQLBatchSize, so the complexity ceiling (250 complexity / ≈14
// per MR → safe up to 17) is never hit. Uses a fake transport that returns a
// complexity error for any query with more than 17 MR aliases.
func TestGraphQLBatchingUnderCeiling(t *testing.T) {
	const n = 20 // > 17 would trigger the old bug; batchSize=12 keeps each batch safe

	p, err := New(sdk.ConnectionConfig{Type: "gitlab", Token: "test-token", BaseURL: testBaseURL})
	if err != nil {
		t.Fatal(err)
	}
	p.http.Transport = &complexityCeilingRT{maxItems: 17}
	p.handle = "octocat"

	events := make([]sdk.Event, n)
	for i := range n {
		mr := makeMR("not_approved")
		mr.IID = i + 1
		mr.WebURL = fmt.Sprintf("https://gitlab.com/acme/api/-/merge_requests/%d", mr.IID)
		mr.References.Full = fmt.Sprintf("acme/api!%d", mr.IID)
		events[i] = p.observedFromMR(mr)
	}

	out, degraded := p.graphqlJoin(context.Background(), events)
	if degraded {
		t.Fatal("graphqlJoin degraded with 20 MRs: complexity ceiling hit — batch size too large")
	}
	// Every event should be enriched: the fake transport returns approvedBy.count=1
	// and approvedBy.nodes=[{username:"octocat"}], so MyReviewState="approved".
	for i, ev := range out {
		pl, ok := ev.Payload.(sdk.ItemObservedPayload)
		if !ok {
			t.Errorf("event %d: payload type %T", i, ev.Payload)
			continue
		}
		if pl.MyReviewState != reviewStateApproved {
			t.Errorf("event %d (%s): MyReviewState=%q, want approved — not enriched by GraphQL",
				i, ev.NativeID, pl.MyReviewState)
		}
	}
}

// TestDoGraphQLErrorsField verifies that doGraphQL surfaces a HTTP-200 response
// with a non-empty errors field and null data as a *graphQLError instead of
// silently returning nil data (the old behaviour that hid complexity rejections).
func TestDoGraphQLErrorsField(t *testing.T) {
	p := newStubProvider(t, stubRT{
		status: 200,
		body:   `{"data":null,"errors":[{"message":"Query has complexity of 420 which exceeds max complexity of 250"}]}`,
	})
	_, err := p.doGraphQL(context.Background(), `query{a0:project(fullPath:"x"){mergeRequest(iid:"1"){approved}}}`)
	if err == nil {
		t.Fatal("doGraphQL: expected error when errors field is non-empty and data is null, got nil")
	}
	var gErr *graphQLError
	if !errors.As(err, &gErr) {
		t.Fatalf("doGraphQL: err type = %T (%v), want *graphQLError", err, err)
	}
	if !strings.Contains(gErr.Error(), "complexity") {
		t.Errorf("error message %q should mention complexity", gErr.Error())
	}
}

// reconcileDegradeRT serves a single opened MR on the REST merge_requests
// endpoint and rejects every GraphQL query with a GitLab-style complexity
// error, so a Reconcile always fetches items but their enrichment always
// degrades.
type reconcileDegradeRT struct{ mrsJSON []byte }

func (rt reconcileDegradeRT) RoundTrip(req *http.Request) (*http.Response, error) {
	if strings.Contains(req.URL.Path, "graphql") {
		resp := `{"data":null,"errors":[{"message":"Query has complexity of 420 which exceeds max complexity of 250"}]}`
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(resp)),
		}, nil
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(bytes.NewReader(rt.mrsJSON)),
	}, nil
}

// TestReconcileDegradedHoldsCursors verifies C1: when this cycle's GraphQL
// enrichment degrades, Reconcile must not advance any scope cursor — the
// returned State must equal the input state so the next reconcile re-fetches
// and retries the un-enriched items instead of skipping them.
func TestReconcileDegradedHoldsCursors(t *testing.T) {
	mrsJSON, err := json.Marshal([]glMergeRequest{makeMR("not_approved")})
	if err != nil {
		t.Fatal(err)
	}

	p, err := New(sdk.ConnectionConfig{Type: "gitlab", Token: "test-token", BaseURL: testBaseURL})
	if err != nil {
		t.Fatal(err)
	}
	p.http.Transport = reconcileDegradeRT{mrsJSON: mrsJSON}
	p.handle = "octocat"

	// Seed a prior cursor for every scope so we can prove none of them move.
	in := sdk.PollState{}
	for _, q := range p.reconcileQueries() {
		in[cursorRecQuery(q)] = "2026-07-01T00:00:00Z"
	}

	res, err := p.Reconcile(context.Background(), in)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if !res.Degraded {
		t.Fatal("Reconcile should report Degraded when the GraphQL join hits the complexity ceiling")
	}
	for _, q := range p.reconcileQueries() {
		key := cursorRecQuery(q)
		if got := res.State[key]; got != in[key] {
			t.Errorf("cursor %q advanced to %q on a degraded enrichment; want held at %q", key, got, in[key])
		}
	}
}
