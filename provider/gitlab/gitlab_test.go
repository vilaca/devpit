package gitlab

import (
	"context"
	"errors"
	"testing"

	"github.com/vilaca/devpit/sdk"
	"gopkg.in/dnaeon/go-vcr.v3/recorder"
)

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
	t.Cleanup(func() { rec.Stop() })

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
			pl := e.Payload.(sdk.ItemObservedPayload)
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

func TestRegistered(t *testing.T) {
	if _, ok := sdk.Registry["gitlab"]; !ok {
		t.Fatal("gitlab not registered")
	}
}
