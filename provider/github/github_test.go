package github

import (
	"context"
	"errors"
	"testing"

	"github.com/vilaca/devpit/sdk"
	"gopkg.in/dnaeon/go-vcr.v3/recorder"
)

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
			pl := e.Payload.(sdk.ItemObservedPayload)
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
			observedByID[e.NativeID] = e.Payload.(sdk.ItemObservedPayload)
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

func TestRegistered(t *testing.T) {
	if _, ok := sdk.Registry["github"]; !ok {
		t.Fatal("github not registered")
	}
}
