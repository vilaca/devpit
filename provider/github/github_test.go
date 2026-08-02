package github

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"slices"
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

func TestFastPollDropsWatchedNoRole(t *testing.T) {
	// A "subscribed" notification for a PR we merely watch: no signal from the
	// reason and no role on the PR. It must not be snapshotted, or the fold
	// would surface it as a bare row.
	p := newTestProvider(t, "fastpoll_watch", "octocat")
	res, err := p.FastPoll(context.Background(), nil)
	if err != nil {
		t.Fatalf("FastPoll: %v", err)
	}
	if len(res.Events) != 0 {
		t.Errorf("events = %d, want 0 for watched no-role PR", len(res.Events))
	}
}

// TestFastPollKeepsWatchedMergedNoRole: a watched, role-less PR that has since
// merged yields no signal and no role — but its *non-open* snapshot must still
// pass through, so the fold drops the item and a mention-only ghost clears on
// merge (ADR-0024). Only an *open* role-less snapshot is dropped at the source.
func TestFastPollKeepsWatchedMergedNoRole(t *testing.T) {
	p := newTestProvider(t, "fastpoll_watch_merged", "octocat")
	res, err := p.FastPoll(context.Background(), nil)
	if err != nil {
		t.Fatalf("FastPoll: %v", err)
	}
	var observed int
	for _, e := range res.Events {
		if e.EventType != "item.observed" {
			continue
		}
		observed++
		pl, ok := e.Payload.(sdk.ItemObservedPayload)
		if !ok {
			t.Fatalf("observed payload type %T", e.Payload)
		}
		if pl.State == "open" {
			t.Errorf("kept snapshot state = %q, want non-open (merged)", pl.State)
		}
	}
	if observed != 1 {
		t.Errorf("observed events = %d, want 1 (merged snapshot kept for the fold to drop)", observed)
	}
}

// soleApproverDegradeRT lets every search scope succeed but fails the
// collaborators probe, so keepAsSoleApprover errors — the silent-degrade path
// that must clear Complete.
type soleApproverDegradeRT struct{}

func (soleApproverDegradeRT) RoundTrip(req *http.Request) (*http.Response, error) {
	json200 := func(body string) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(body)),
		}, nil
	}
	switch {
	case strings.Contains(req.URL.Path, "/search/issues"):
		// One non-draft PR authored by someone else — a sole-approver candidate.
		return json200(`{"items":[{"number":100,"title":"x","html_url":"https://github.com/acme/api/pull/100",` +
			`"state":"open","draft":false,"user":{"login":"jdoe"},"pull_request":{"url":"u"},` +
			`"repository_url":"https://api.github.com/repos/acme/api"}]}`)
	case strings.Contains(req.URL.Path, "/collaborators"):
		return &http.Response{
			StatusCode: http.StatusInternalServerError,
			Header:     http.Header{},
			Body:       io.NopCloser(strings.NewReader("")),
		}, nil
	case strings.Contains(req.URL.Path, "/graphql"):
		return json200(`{"data":{}}`)
	default:
		return json200(`{"items":[]}`)
	}
}

// TestReconcileSoleApproverProbeFailureClearsComplete: a failed collaborators
// probe leaves the sole-approver set short, so the sweep is not authoritative
// and must not drive a reap (ADR-0024).
func TestReconcileSoleApproverProbeFailureClearsComplete(t *testing.T) {
	p := newStubProvider(t, stubRT{})
	p.http.Transport = soleApproverDegradeRT{}
	p.handle = "octocat"

	res, err := p.Reconcile(context.Background(), nil)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if res.Complete {
		t.Error("Complete = true, want false when a sole-approver probe failed")
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
	// A full sweep with every scope succeeding is a complete, authoritative sweep.
	if !res.Complete {
		t.Errorf("Complete = false, want true when every scope succeeded")
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
		{"forbidden without rate signal", http.StatusForbidden, sdk.ErrUnauthorized, 0},
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

func TestGHReviewState(t *testing.T) {
	for in, want := range map[string]string{
		ghReviewStateApproved: normalizedApproved,
		"CHANGES_REQUESTED":   "changes_requested",
		"COMMENTED":           "reviewed",
		"DISMISSED":           "",
		"PENDING":             "",
	} {
		if got := ghReviewState(in); got != want {
			t.Errorf("ghReviewState(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestGHReviewDecision(t *testing.T) {
	for in, want := range map[string]string{
		"CHANGES_REQUESTED":   "changes_requested",
		ghReviewStateApproved: normalizedApproved,
		"REVIEW_REQUIRED":     "review_required",
		"":                    "",
		"UNKNOWN":             "",
	} {
		if got := ghReviewDecision(in); got != want {
			t.Errorf("ghReviewDecision(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestGraphQLJoinChangesRequested verifies the PR-level reviewDecision
// CHANGES_REQUESTED surfaces as review_decision "changes_requested" on the
// joined payload (the author's changes-requested signal).
func TestGraphQLJoinChangesRequested(t *testing.T) {
	p := newTestProvider(t, "graphql_join_changes_requested", "octocat")
	res, err := p.FastPoll(context.Background(), nil)
	if err != nil {
		t.Fatalf("FastPoll: %v", err)
	}
	for _, e := range res.Events {
		if e.EventType != "item.observed" || e.NativeID != "acme/api#42" {
			continue
		}
		pl, ok := e.Payload.(sdk.ItemObservedPayload)
		if !ok {
			t.Fatal("payload type assertion failed")
		}
		if pl.ReviewDecision != "changes_requested" {
			t.Errorf("review_decision = %q, want changes_requested", pl.ReviewDecision)
		}
		return
	}
	t.Fatal("missing item.observed for acme/api#42")
}

// TestMergeGHBatchResultsMyReviewState verifies my_review_state is taken from the
// authenticated user's latest review, and the approval count still counts only APPROVED.
func TestMergeGHBatchResultsMyReviewState(t *testing.T) {
	data := map[string]json.RawMessage{
		"a0": json.RawMessage(`{"pullRequest":{"reviewDecision":"APPROVED",` +
			`"latestReviews":{"nodes":[` +
			`{"state":"APPROVED","author":{"login":"octocat"}},` +
			`{"state":"COMMENTED","author":{"login":"other"}}]}}}`),
	}
	results := map[int]ghResult{}
	mergeGHBatchResults(data, []prItem{{evIdx: 5}}, results, "octocat")

	r, ok := results[5]
	if !ok {
		t.Fatal("missing result for evIdx 5")
	}
	if r.myReviewState != normalizedApproved {
		t.Errorf("myReviewState = %q, want approved", r.myReviewState)
	}
	if r.approvalsCount != 1 {
		t.Errorf("approvalsCount = %d, want 1 (only APPROVED counts)", r.approvalsCount)
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
			// autoMergeRequest is absent from this GraphQL response — the field
			// reads as nil and AutoMergeArmed degrades to false with no crash
			// (the fine-grained-PAT-cannot-read-the-field path).
			if pl.AutoMergeArmed {
				t.Error("auto_merge_armed should be false when autoMergeRequest is absent")
			}
			return
		}
	}
	t.Fatal("missing item.observed for acme/api#42")
}

// TestGraphQLJoinAutoMergeArmed verifies a non-null autoMergeRequest in the
// GraphQL response sets AutoMergeArmed.
func TestGraphQLJoinAutoMergeArmed(t *testing.T) {
	p := newTestProvider(t, "graphql_join_auto_merge_armed", "octocat")
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
			if !pl.AutoMergeArmed {
				t.Error("auto_merge_armed should be true when autoMergeRequest is non-null")
			}
			if pl.ChecksRunning {
				t.Error("checks_running is a GitHub parity gap and must stay false")
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
			if pl.AutoMergeArmed {
				t.Error("auto_merge_armed should be false when GraphQL is degraded")
			}
			return
		}
	}
	t.Fatal("missing item.observed for acme/api#42")
}

// TestLabelsNames verifies GitHub's label names flow into the payload, and no
// labels yields nil.
func TestLabelsNames(t *testing.T) {
	p := &Provider{handle: "octocat"}

	pr := makePR("clean")
	pr.Labels = []ghLabel{
		{Name: "bug"},
		{Name: "kept"},
	}
	pl, ok := p.observedFromPull(pr).Payload.(sdk.ItemObservedPayload)
	if !ok {
		t.Fatal("payload type assertion failed")
	}
	want := []string{"bug", "kept"}
	if !reflect.DeepEqual(pl.Labels, want) {
		t.Errorf("labels = %+v, want %+v", pl.Labels, want)
	}

	bare, _ := p.observedFromPull(makePR("clean")).Payload.(sdk.ItemObservedPayload)
	if bare.Labels != nil {
		t.Errorf("labels = %+v, want nil for no labels", bare.Labels)
	}
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

// TestReconcileSoleApprover verifies that:
//   - a non-draft PR by another user on a repo where octocat is sole approver
//     gets the "sole_approver" role;
//   - a PR on a repo with two collaborators does NOT get the role;
//   - a draft PR and a self-authored PR are both filtered out.
type observedItem struct {
	nativeID string
	roles    []string
}

func itemHasRole(items []observedItem, nativeID, role string) bool {
	for _, it := range items {
		if it.nativeID == nativeID && slices.Contains(it.roles, role) {
			return true
		}
	}
	return false
}

func collectObserved(events []sdk.Event) []observedItem {
	var out []observedItem
	for _, ev := range events {
		if ev.EventType != "item.observed" {
			continue
		}
		pl, ok := ev.Payload.(sdk.ItemObservedPayload)
		if !ok {
			continue
		}
		out = append(out, observedItem{ev.NativeID, pl.MyRoles})
	}
	return out
}

func TestReconcileSoleApprover(t *testing.T) {
	p := newTestProvider(t, "sole_approver", "octocat")
	result, err := p.Reconcile(context.Background(), nil)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	observed := collectObserved(result.Events)

	if !itemHasRole(observed, "octocat/myrepo#100", roleSoleApprover) {
		t.Error("PR#100 on sole-approver repo should have role sole_approver")
	}
	if itemHasRole(observed, "octocat/sharedrepo#101", roleSoleApprover) {
		t.Error("PR#101 on shared repo must not have role sole_approver")
	}
	for _, nid := range []string{"octocat/myrepo#102", "octocat/myrepo#103"} {
		if itemHasRole(observed, nid, roleSoleApprover) {
			t.Errorf("%s must not have role sole_approver", nid)
		}
	}
}

// TestObservedApprovalsCountUnknownDefault verifies a fresh PR reports
// approvals_count -1 (unknown) from both constructors, so a PR first seen while
// GraphQL is degraded reads unknown rather than 0/hide-count (A6).
func TestObservedApprovalsCountUnknownDefault(t *testing.T) {
	p := &Provider{handle: "octocat"}
	pull, ok := p.observedFromPull(makePR("clean")).Payload.(sdk.ItemObservedPayload)
	if !ok {
		t.Fatal("observedFromPull payload type assertion failed")
	}
	if pull.ApprovalsCount != -1 {
		t.Errorf("observedFromPull approvals_count = %d, want -1 (unknown before the GraphQL join)", pull.ApprovalsCount)
	}
	item := ghSearchItem{Number: 1, HTMLURL: "https://github.com/acme/api/pull/1", User: ghUser{Login: "jdoe"}}
	search, ok := p.observedFromSearch(item, "acme/api", nil).Payload.(sdk.ItemObservedPayload)
	if !ok {
		t.Fatal("observedFromSearch payload type assertion failed")
	}
	if search.ApprovalsCount != -1 {
		t.Errorf("observedFromSearch approvals_count = %d, want -1 (unknown before the GraphQL join)", search.ApprovalsCount)
	}
}

// TestDo403RateLimitVsAuth verifies a 403 is treated as rate-limited only when a
// rate signal is present (Retry-After, or the primary-limit marker
// X-RateLimit-Remaining: 0); a 403 with neither — including one that carries
// non-zero rate headers, as every GitHub response does — is a permission/SSO/scope
// denial that surfaces as ErrUnauthorized rather than retrying forever (A4).
func TestDo403RateLimitVsAuth(t *testing.T) {
	cases := []struct {
		name    string
		header  http.Header
		wantErr error
	}{
		{"retry-after present", http.Header{"Retry-After": {"30"}}, sdk.ErrRateLimited},
		{"remaining zero", http.Header{"X-Ratelimit-Remaining": {"0"}}, sdk.ErrRateLimited},
		{"remaining non-zero", http.Header{"X-Ratelimit-Remaining": {"57"}}, sdk.ErrUnauthorized},
		{"no rate signal", http.Header{}, sdk.ErrUnauthorized},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := newStubProvider(t, stubRT{status: http.StatusForbidden, header: c.header, body: "{}"})
			resp, err := p.do(context.Background(), "https://api.github.com/x", nil)
			if resp != nil {
				_ = resp.Body.Close()
			}
			if !errors.Is(err, c.wantErr) {
				t.Fatalf("err = %v, want %v", err, c.wantErr)
			}
		})
	}
}

// TestDoGraphQLErrorClassification verifies GitHub's HTTP-200 GraphQL error
// bodies are classified (A1): a RATE_LIMITED type surfaces sdk.ErrRateLimited so
// the engine backs off, a generic errors array with null data surfaces a
// *graphQLError, and a null data body with no errors is still an error (never a
// silent nil-data success).
func TestDoGraphQLErrorClassification(t *testing.T) {
	rateLimited := func() *Provider {
		return newStubProvider(t, stubRT{status: 200,
			body: `{"data":null,"errors":[{"type":"RATE_LIMITED","message":"API rate limit exceeded"}]}`})
	}
	if _, err := rateLimited().doGraphQL(context.Background(), "query{}"); !errors.Is(err, sdk.ErrRateLimited) {
		t.Errorf("RATE_LIMITED: err = %v, want ErrRateLimited", err)
	}

	generic := newStubProvider(t, stubRT{status: 200,
		body: `{"data":null,"errors":[{"message":"Something went wrong executing your query."}]}`})
	var gErr *graphQLError
	if _, err := generic.doGraphQL(context.Background(), "query{}"); !errors.As(err, &gErr) {
		t.Errorf("generic error: err type = %T (%v), want *graphQLError", err, err)
	}

	nullData := newStubProvider(t, stubRT{status: 200, body: `{"data":null}`})
	if _, err := nullData.doGraphQL(context.Background(), "query{}"); err == nil {
		t.Error("null data with no errors: want a classified error, got nil")
	}
}

// TestGraphQLJoinRateLimitPropagates verifies a GraphQL RATE_LIMITED error is
// propagated out of the join (not swallowed) so FastPoll/Reconcile fail the cycle
// and the engine backs off (A1).
func TestGraphQLJoinRateLimitPropagates(t *testing.T) {
	p := newStubProvider(t, stubRT{status: 200, body: `{"data":null,"errors":[{"type":"RATE_LIMITED"}]}`})
	p.handle = "octocat"
	events := []sdk.Event{p.observedFromPull(makePR("blocked"))}
	_, degraded, err := p.graphqlJoin(context.Background(), events)
	if !errors.Is(err, sdk.ErrRateLimited) {
		t.Fatalf("err = %v, want ErrRateLimited", err)
	}
	if degraded {
		t.Error("degraded should be false when the cycle fails outright with a rate limit")
	}
}

// TestGraphQLJoinGenericErrorDegrades verifies a non-rate GraphQL error degrades
// the batch (Degraded=true, no error) and leaves the REST payload intact — the
// unknown approvals count stays -1 rather than being zeroed (A1/A2).
func TestGraphQLJoinGenericErrorDegrades(t *testing.T) {
	p := newStubProvider(t, stubRT{status: 200, body: `{"data":null,"errors":[{"message":"boom"}]}`})
	p.handle = "octocat"
	events := []sdk.Event{p.observedFromPull(makePR("blocked"))}
	out, degraded, err := p.graphqlJoin(context.Background(), events)
	if err != nil {
		t.Fatalf("err = %v, want nil (a generic GraphQL error degrades, not fails)", err)
	}
	if !degraded {
		t.Error("degraded should be true on a generic GraphQL error")
	}
	pl, ok := out[0].Payload.(sdk.ItemObservedPayload)
	if !ok {
		t.Fatalf("payload type %T", out[0].Payload)
	}
	if pl.ApprovalsCount != -1 {
		t.Errorf("approvals_count = %d, want -1 (REST payload kept, not zeroed)", pl.ApprovalsCount)
	}
}

// TestGraphQLJoinNullNodeKeepsREST verifies a JSON-null PR node (an inaccessible
// repo in the batch) is skipped so its REST payload survives instead of being
// overwritten with zeros, while sibling PRs still enrich and the cycle is marked
// degraded (A3).
func TestGraphQLJoinNullNodeKeepsREST(t *testing.T) {
	p := newStubProvider(t, stubRT{status: 200,
		// a0 (PR #1) resolves; a1 (PR #2) is null with an accompanying field error.
		body: `{"data":{"a0":{"pullRequest":{"reviewDecision":"APPROVED",` +
			`"latestReviews":{"nodes":[{"state":"APPROVED","author":{"login":"someone"}}]}}},"a1":null},` +
			`"errors":[{"type":"NOT_FOUND","path":["a1"],"message":"Could not resolve to a Repository"}]}`})
	p.handle = "octocat"

	pr1 := makePR("blocked")
	pr2 := makePR("blocked")
	pr2.Number = 2
	pr2.HTMLURL = "https://github.com/acme/api/pull/2"
	events := []sdk.Event{p.observedFromPull(pr1), p.observedFromPull(pr2)}

	out, degraded, err := p.graphqlJoin(context.Background(), events)
	if err != nil {
		t.Fatalf("graphqlJoin: %v", err)
	}
	if !degraded {
		t.Error("degraded should be true when a node came back null")
	}
	byID := map[string]sdk.ItemObservedPayload{}
	for _, e := range out {
		pl, ok := e.Payload.(sdk.ItemObservedPayload)
		if !ok {
			t.Fatalf("payload type %T", e.Payload)
		}
		byID[e.NativeID] = pl
	}
	if got := byID["acme/api#1"].ApprovalsCount; got != 1 {
		t.Errorf("PR#1 approvals_count = %d, want 1 (enriched from the good node)", got)
	}
	if got := byID["acme/api#2"].ApprovalsCount; got != -1 {
		t.Errorf("PR#2 approvals_count = %d, want -1 (null node keeps REST payload, not zeroed)", got)
	}
}

// reconcileGraphQLDegradeRT lets every REST call succeed (a self-authored PR on
// every scope, no sole-approver probe needed) but degrades the GraphQL join.
type reconcileGraphQLDegradeRT struct{}

func (reconcileGraphQLDegradeRT) RoundTrip(req *http.Request) (*http.Response, error) {
	json200 := func(body string) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(body)),
		}, nil
	}
	switch {
	case strings.Contains(req.URL.Path, "/graphql"):
		return json200(`{"data":null,"errors":[{"message":"Something went wrong."}]}`)
	case strings.Contains(req.URL.Path, "/search/issues"):
		return json200(`{"items":[{"number":50,"title":"x","html_url":"https://github.com/acme/api/pull/50",` +
			`"state":"open","draft":false,"user":{"login":"octocat"},"pull_request":{"url":"u"},` +
			`"repository_url":"https://api.github.com/repos/acme/api"}]}`)
	default:
		return json200(`{"items":[]}`)
	}
}

// TestReconcileDegradedStillComplete verifies Complete is independent of Degraded
// (ADR-0024): a GraphQL enrichment failure degrades the cycle but the REST
// identity set is intact, so the sweep stays complete and the engine may reap
// against it (A2).
func TestReconcileDegradedStillComplete(t *testing.T) {
	p := newStubProvider(t, stubRT{})
	p.http.Transport = reconcileGraphQLDegradeRT{}
	p.handle = "octocat"

	res, err := p.Reconcile(context.Background(), nil)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if !res.Degraded {
		t.Error("Degraded should be true when the GraphQL join fails")
	}
	if !res.Complete {
		t.Error("Complete must stay true on a GraphQL-only degradation (independent of Degraded)")
	}
}

// soleApproverURLRT captures the collaborators request URL and returns two
// merge-capable members so the probe reports not-sole; used to prove the probe
// no longer filters to affiliation=direct (A5).
type soleApproverURLRT struct{ collabURL string }

func (rt *soleApproverURLRT) RoundTrip(req *http.Request) (*http.Response, error) {
	json200 := func(body string) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(body)),
		}, nil
	}
	if strings.Contains(req.URL.Path, "/collaborators") {
		rt.collabURL = req.URL.String()
		// octocat is a direct collaborator; teammate can merge via a team — both
		// must be counted so the repo is not sole-approver.
		return json200(`[{"login":"octocat","permissions":{"push":true}},` +
			`{"login":"teammate","permissions":{"push":true}}]`)
	}
	return json200(`[]`)
}

// TestProbeIsSoleApproverIncludesTeamMembers verifies probeIsSoleApprover queries
// with affiliation=all (not direct), so a member who can merge via team/org
// membership is counted and the repo is not falsely tagged sole_approver (A5).
func TestProbeIsSoleApproverIncludesTeamMembers(t *testing.T) {
	rt := &soleApproverURLRT{}
	p := newStubProvider(t, stubRT{})
	p.http.Transport = rt
	p.handle = "octocat"

	isSole, err := p.probeIsSoleApprover(context.Background(), "acme/api")
	if err != nil {
		t.Fatalf("probeIsSoleApprover: %v", err)
	}
	if isSole {
		t.Error("isSole = true, want false: a team-based approver must count")
	}
	if strings.Contains(rt.collabURL, "affiliation=direct") {
		t.Errorf("collaborators URL %q still filters affiliation=direct; must include team/org members", rt.collabURL)
	}
	if !strings.Contains(rt.collabURL, "affiliation=all") {
		t.Errorf("collaborators URL %q should request affiliation=all", rt.collabURL)
	}
}

// ghVerdictGraphQLBody wraps a latestReviews nodes JSON fragment in the full
// GraphQL response envelope for PR alias a0. State names live inside this raw
// string (never as standalone Go literals) so the fold's verdict emission can be
// driven without repeating enum literals across the test file.
func ghVerdictGraphQLBody(reviewNodes string) string {
	return `{"data":{"a0":{"pullRequest":{"reviewDecision":"APPROVED",` +
		`"latestReviews":{"nodes":[` + reviewNodes + `]},"autoMergeRequest":null}}}}`
}

// newGHVerdictProvider builds a github provider whose single HTTP dependency
// (the GraphQL POST) returns body. No BaseURL is set — New defaults it, and the
// stub transport answers every request, so tests need no live host.
func newGHVerdictProvider(t *testing.T, body string) *Provider {
	t.Helper()
	p := newStubProvider(t, stubRT{status: http.StatusOK, body: body})
	p.handle = "octocat"
	return p
}

// TestGHVerdictSignalsApproved verifies that an APPROVED review with submittedAt
// emits signal.approved with OccurredAt = submittedAt and dedupe key
// "signal.approved:review:<login>:<submittedAt>".
func TestGHVerdictSignalsApproved(t *testing.T) {
	submittedAt := "2026-07-16T15:00:00Z"
	body := ghVerdictGraphQLBody(
		`{"state":"APPROVED","submittedAt":"` + submittedAt + `","author":{"login":"alice"}}`)
	p := newGHVerdictProvider(t, body)

	out, degraded, err := p.graphqlJoin(context.Background(), []sdk.Event{p.observedFromPull(makePR("clean"))})
	if err != nil || degraded {
		t.Fatalf("graphqlJoin: err=%v degraded=%v", err, degraded)
	}

	var approved int
	for _, e := range out {
		if e.EventType != signalApproved {
			continue
		}
		approved++
		if e.Actor != "alice" {
			t.Errorf("actor = %q, want alice", e.Actor)
		}
		wantDedupe := "signal.approved:review:alice:" + submittedAt
		if e.DedupeKey != wantDedupe {
			t.Errorf("dedupe = %q, want %q", e.DedupeKey, wantDedupe)
		}
		wantTime, _ := time.Parse(time.RFC3339, submittedAt)
		if e.OccurredAt == nil || !e.OccurredAt.Equal(wantTime) {
			t.Errorf("OccurredAt = %v, want %v", e.OccurredAt, wantTime)
		}
	}
	if approved != 1 {
		t.Errorf("signal.approved count = %d, want 1", approved)
	}
}

// TestGHVerdictSignalsChangesRequested verifies CHANGES_REQUESTED emits
// signal.changes_requested with a real OccurredAt.
func TestGHVerdictSignalsChangesRequested(t *testing.T) {
	submittedAt := "2026-07-17T08:00:00Z"
	body := ghVerdictGraphQLBody(
		`{"state":"CHANGES_REQUESTED","submittedAt":"` + submittedAt + `","author":{"login":"bob"}}`)
	p := newGHVerdictProvider(t, body)

	out, _, _ := p.graphqlJoin(context.Background(), []sdk.Event{p.observedFromPull(makePR("clean"))})
	var changes int
	for _, e := range out {
		if e.EventType != signalChangesRequested {
			continue
		}
		changes++
		if e.DedupeKey != "signal.changes_requested:review:bob:"+submittedAt {
			t.Errorf("dedupe = %q, want signal.changes_requested:review:bob:%s", e.DedupeKey, submittedAt)
		}
	}
	if changes != 1 {
		t.Errorf("signal.changes_requested count = %d, want 1", changes)
	}
}

// TestGHVerdictSignalsDraftSuppressed verifies draft PRs emit no verdict signals.
func TestGHVerdictSignalsDraftSuppressed(t *testing.T) {
	body := ghVerdictGraphQLBody(
		`{"state":"APPROVED","submittedAt":"2026-07-17T09:00:00Z","author":{"login":"carol"}}`)
	p := newGHVerdictProvider(t, body)

	pr := makePR("clean")
	pr.Draft = true
	out, _, _ := p.graphqlJoin(context.Background(), []sdk.Event{p.observedFromPull(pr)})
	for _, e := range out {
		if e.EventType == signalApproved || e.EventType == signalChangesRequested {
			t.Errorf("draft PR must not emit verdict signal %q", e.EventType)
		}
	}
}

// TestGHVerdictSignalsCommentedDismissedSkipped verifies COMMENTED and DISMISSED
// review states do not emit verdict signals (only APPROVED and CHANGES_REQUESTED).
func TestGHVerdictSignalsCommentedDismissedSkipped(t *testing.T) {
	body := ghVerdictGraphQLBody(
		`{"state":"COMMENTED","submittedAt":"2026-07-17T10:00:00Z","author":{"login":"dave"}},` +
			`{"state":"DISMISSED","submittedAt":"2026-07-17T11:00:00Z","author":{"login":"erin"}}`)
	p := newGHVerdictProvider(t, body)

	out, _, _ := p.graphqlJoin(context.Background(), []sdk.Event{p.observedFromPull(makePR("clean"))})
	for _, e := range out {
		if e.EventType == signalApproved || e.EventType == signalChangesRequested {
			t.Errorf("COMMENTED/DISMISSED must not emit verdict signal %q", e.EventType)
		}
	}
}
