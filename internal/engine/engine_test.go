package engine

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/vilaca/devpit/internal/storage"
	"github.com/vilaca/devpit/sdk"
)

// --- Fakes -----------------------------------------------------------------

// fakeStore implements Store with recorded calls and injectable errors. All
// access is guarded so the Run test (which touches it from a goroutine) is
// race-free.
type fakeStore struct {
	mu sync.Mutex

	cursors sdk.PollState

	loadErr    error
	writeErr   error
	saveErr    error
	syncLogErr error
	factsErr   error

	inserted int // WriteEvents return value

	facts         []storage.ItemFact // LatestItemFacts return value
	eventsWritten [][]sdk.Event
	cursorsSaved  []sdk.PollState
	logs          []storage.SyncLogEntry
}

func (f *fakeStore) LoadCursors(_ context.Context, _ string) (sdk.PollState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.loadErr != nil {
		return nil, f.loadErr
	}
	if f.cursors == nil {
		return sdk.PollState{}, nil
	}
	return f.cursors, nil
}

func (f *fakeStore) SaveCursors(_ context.Context, _ string, state sdk.PollState) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.saveErr != nil {
		return f.saveErr
	}
	f.cursorsSaved = append(f.cursorsSaved, state)
	return nil
}

func (f *fakeStore) WriteEvents(_ context.Context, _ string, events []sdk.Event) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.writeErr != nil {
		return 0, f.writeErr
	}
	f.eventsWritten = append(f.eventsWritten, events)
	return f.inserted, nil
}

func (f *fakeStore) LatestItemFacts(_ context.Context, _ string) ([]storage.ItemFact, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.facts, f.factsErr
}

func (f *fakeStore) WriteSyncLog(_ context.Context, entry storage.SyncLogEntry) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.logs = append(f.logs, entry)
	return f.syncLogErr
}

func (f *fakeStore) lastLog(t *testing.T) storage.SyncLogEntry {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.logs) == 0 {
		t.Fatal("expected a sync_log row, got none")
	}
	return f.logs[len(f.logs)-1]
}

func (f *fakeStore) logCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.logs)
}

// fakeProvider implements sdk.Provider with injectable results and a hook that
// fires inside FastPoll/Reconcile (used to cancel mid-call).
type fakeProvider struct {
	mu sync.Mutex

	caps        sdk.Capabilities
	identity    sdk.Identity
	identityErr error

	result   sdk.PollResult
	pollErr  error
	closeErr error

	hook func() // runs at the top of FastPoll/Reconcile

	fastCalls  int
	reconCalls int
	closed     bool
}

func (p *fakeProvider) ResolveIdentity(context.Context) (sdk.Identity, error) {
	return p.identity, p.identityErr
}

func (p *fakeProvider) Capabilities() sdk.Capabilities { return p.caps }

func (p *fakeProvider) FastPoll(context.Context, sdk.PollState) (sdk.PollResult, error) {
	p.mu.Lock()
	p.fastCalls++
	p.mu.Unlock()
	if p.hook != nil {
		p.hook()
	}
	return p.result, p.pollErr
}

func (p *fakeProvider) Reconcile(context.Context, sdk.PollState) (sdk.PollResult, error) {
	p.mu.Lock()
	p.reconCalls++
	p.mu.Unlock()
	if p.hook != nil {
		p.hook()
	}
	return p.result, p.pollErr
}

func (p *fakeProvider) Close(context.Context) error {
	p.mu.Lock()
	p.closed = true
	p.mu.Unlock()
	return p.closeErr
}

func (p *fakeProvider) polls() (fast, recon int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.fastCalls, p.reconCalls
}

// recordNotifier implements Notifier and counts every event.
type recordNotifier struct {
	mu        sync.Mutex
	attention int
	completed []string
	failed    []string // "connID: cause"
}

func (n *recordNotifier) AttentionChanged() {
	n.mu.Lock()
	n.attention++
	n.mu.Unlock()
}

func (n *recordNotifier) SyncCompleted(connID string) {
	n.mu.Lock()
	n.completed = append(n.completed, connID)
	n.mu.Unlock()
}

func (n *recordNotifier) SyncFailed(connID, cause string) {
	n.mu.Lock()
	n.failed = append(n.failed, connID+": "+cause)
	n.mu.Unlock()
}

func newTestConn(store Store, prov sdk.Provider, notify Notifier) *conn {
	return &conn{
		cfg:          sdk.ConnectionConfig{ID: "c1", Type: "github"},
		prov:         prov,
		caps:         prov.Capabilities(),
		store:        store,
		notify:       notify,
		fastEvery:    time.Minute,
		reconEvery:   15 * time.Minute,
		closeTimeout: time.Second,
		resolved:     true,
	}
}

// --- classify --------------------------------------------------------------

func TestClassify(t *testing.T) {
	status := func(n int) *int { return &n }
	tests := []struct {
		name       string
		err        error
		wantOut    string
		wantStatus *int
	}{
		{"5xx StatusError", &sdk.StatusError{Status: 503}, outcomeServer, status(503)},
		{"stray 4xx StatusError", &sdk.StatusError{Status: 418}, outcomeUnexpected, status(418)},
		{"bare server", sdk.ErrServer, outcomeServer, nil},
		{"bare unexpected", sdk.ErrUnexpected, outcomeUnexpected, nil},
		{"parse", fmt.Errorf("decode: %w", sdk.ErrParse), outcomeParse, nil},
		{"transport", errors.New("dial tcp: i/o timeout"), outcomeNetwork, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotOut, gotStatus := classify(tt.err)
			if gotOut != tt.wantOut {
				t.Errorf("outcome = %q, want %q", gotOut, tt.wantOut)
			}
			switch {
			case tt.wantStatus == nil && gotStatus != nil:
				t.Errorf("status = %d, want nil", *gotStatus)
			case tt.wantStatus != nil && gotStatus == nil:
				t.Errorf("status = nil, want %d", *tt.wantStatus)
			case tt.wantStatus != nil && *gotStatus != *tt.wantStatus:
				t.Errorf("status = %d, want %d", *gotStatus, *tt.wantStatus)
			}
		})
	}
}

// --- cycle: success --------------------------------------------------------

func TestCycleSuccessInserts(t *testing.T) {
	store := &fakeStore{inserted: 2}
	prov := &fakeProvider{result: sdk.PollResult{
		Events: []sdk.Event{{NativeID: "x"}, {NativeID: "y"}},
		State:  sdk.PollState{"rec.cursor": "42"},
	}}
	notify := &recordNotifier{}
	c := newTestConn(store, prov, notify)

	c.cycle(context.Background(), opReconcile, false)

	log := store.lastLog(t)
	if log.Outcome != outcomeOK {
		t.Errorf("outcome = %q, want ok", log.Outcome)
	}
	if log.Operation != "reconcile" {
		t.Errorf("operation = %q, want reconcile", log.Operation)
	}
	if log.ItemsChanged != 2 {
		t.Errorf("items_changed = %d, want 2", log.ItemsChanged)
	}
	if len(store.cursorsSaved) != 1 {
		t.Fatalf("cursors saved %d times, want 1", len(store.cursorsSaved))
	}
	if store.cursorsSaved[0]["rec.cursor"] != "42" {
		t.Errorf("saved cursor = %v, want rec.cursor=42", store.cursorsSaved[0])
	}
	if notify.attention != 1 {
		t.Errorf("AttentionChanged fired %d times, want 1", notify.attention)
	}
	if len(notify.completed) != 1 {
		t.Errorf("SyncCompleted fired %d times, want 1", len(notify.completed))
	}
	if c.bo.streak != 0 {
		t.Errorf("streak = %d, want 0 after success", c.bo.streak)
	}
}

// TestCycleSuccessNoInsert: a clean cycle that inserts nothing (all deduped)
// still reports SyncCompleted but must not fire the global AttentionChanged.
func TestCycleSuccessNoInsert(t *testing.T) {
	store := &fakeStore{inserted: 0}
	prov := &fakeProvider{result: sdk.PollResult{
		Events: []sdk.Event{{NativeID: "dupe"}},
	}}
	notify := &recordNotifier{}
	c := newTestConn(store, prov, notify)

	c.cycle(context.Background(), opFastPoll, false)

	if notify.attention != 0 {
		t.Errorf("AttentionChanged fired %d times, want 0 when nothing inserted", notify.attention)
	}
	if len(notify.completed) != 1 {
		t.Errorf("SyncCompleted fired %d times, want 1", len(notify.completed))
	}
	if store.lastLog(t).Outcome != outcomeOK {
		t.Error("outcome should be ok")
	}
}

// --- cycle: failures -------------------------------------------------------

func TestCycleProviderErrors(t *testing.T) {
	status := func(n int) *int { return &n }
	tests := []struct {
		name       string
		pollErr    error
		wantOut    string
		wantStatus *int
	}{
		{"server", &sdk.StatusError{Status: 502}, outcomeServer, status(502)},
		{"unexpected", &sdk.StatusError{Status: 418}, outcomeUnexpected, status(418)},
		{"parse", fmt.Errorf("%w", sdk.ErrParse), outcomeParse, nil},
		{"network", errors.New("connection refused"), outcomeNetwork, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &fakeStore{}
			prov := &fakeProvider{pollErr: tt.pollErr}
			notify := &recordNotifier{}
			c := newTestConn(store, prov, notify)

			c.cycle(context.Background(), opReconcile, false)

			log := store.lastLog(t)
			if log.Outcome != tt.wantOut {
				t.Errorf("outcome = %q, want %q", log.Outcome, tt.wantOut)
			}
			if tt.wantStatus == nil && log.HTTPStatus != nil {
				t.Errorf("http_status = %d, want nil", *log.HTTPStatus)
			}
			if tt.wantStatus != nil && (log.HTTPStatus == nil || *log.HTTPStatus != *tt.wantStatus) {
				t.Errorf("http_status = %v, want %d", log.HTTPStatus, *tt.wantStatus)
			}
			if log.Error == nil {
				t.Error("error text should be recorded")
			}
			// Nothing persisted, cursor untouched, backoff bumped, one failure sent.
			if len(store.eventsWritten) != 0 || len(store.cursorsSaved) != 0 {
				t.Error("no events or cursors should be written on error")
			}
			if c.bo.streak != 1 {
				t.Errorf("streak = %d, want 1", c.bo.streak)
			}
			if len(notify.failed) != 1 {
				t.Errorf("SyncFailed fired %d times, want 1", len(notify.failed))
			}
		})
	}
}

func TestCycleAuthFailure(t *testing.T) {
	store := &fakeStore{}
	prov := &fakeProvider{pollErr: sdk.ErrUnauthorized}
	notify := &recordNotifier{}
	c := newTestConn(store, prov, notify)

	c.cycle(context.Background(), opReconcile, false)

	log := store.lastLog(t)
	if log.Outcome != outcomeAuth {
		t.Errorf("outcome = %q, want auth", log.Outcome)
	}
	if c.resolved {
		t.Error("resolved should be cleared after an auth failure so identity re-resolves")
	}
	if c.bo.streak != 1 {
		t.Errorf("streak = %d, want 1", c.bo.streak)
	}
}

func TestCycleRateLimited(t *testing.T) {
	store := &fakeStore{}
	prov := &fakeProvider{pollErr: &sdk.RateLimitError{RetryAfter: 90 * time.Second}}
	notify := &recordNotifier{}
	c := newTestConn(store, prov, notify)

	before := time.Now()
	c.cycle(context.Background(), opReconcile, false)

	log := store.lastLog(t)
	if log.Outcome != outcomeRateLimited {
		t.Errorf("outcome = %q, want rate_limited", log.Outcome)
	}
	if log.NextRetry == nil {
		t.Fatal("next_retry should be set on rate_limited")
	}
	// hint (90s) beats the streak-1 exponential (60s), so retry is ~90s out.
	if got := log.NextRetry.Sub(before); got < 89*time.Second || got > 92*time.Second {
		t.Errorf("next_retry in %s, want ~90s", got)
	}
	if c.bo.streak != 1 {
		t.Errorf("streak = %d, want 1", c.bo.streak)
	}
}

// --- cycle: storage failures ----------------------------------------------

func TestCycleLoadCursorsError(t *testing.T) {
	store := &fakeStore{loadErr: errors.New("db locked")}
	prov := &fakeProvider{}
	notify := &recordNotifier{}
	c := newTestConn(store, prov, notify)

	c.cycle(context.Background(), opReconcile, false)

	if store.lastLog(t).Outcome != outcomeStorage {
		t.Error("outcome should be storage on LoadCursors failure")
	}
	if fast, recon := prov.polls(); fast != 0 || recon != 0 {
		t.Errorf("provider must not be called when cursors fail to load (fast=%d recon=%d)", fast, recon)
	}
	if c.bo.streak != 1 {
		t.Errorf("streak = %d, want 1", c.bo.streak)
	}
}

func TestCycleWriteEventsError(t *testing.T) {
	store := &fakeStore{writeErr: errors.New("disk full")}
	prov := &fakeProvider{result: sdk.PollResult{
		Events: []sdk.Event{{NativeID: "x"}},
		State:  sdk.PollState{"k": "v"},
	}}
	notify := &recordNotifier{}
	c := newTestConn(store, prov, notify)

	c.cycle(context.Background(), opReconcile, false)

	if store.lastLog(t).Outcome != outcomeStorage {
		t.Error("outcome should be storage on WriteEvents failure")
	}
	if len(store.cursorsSaved) != 0 {
		t.Error("cursors must not be saved when WriteEvents fails")
	}
}

// --- cycle: gating & shutdown ---------------------------------------------

func TestCycleBackoffGate(t *testing.T) {
	store := &fakeStore{}
	prov := &fakeProvider{}
	c := newTestConn(store, prov, &recordNotifier{})
	c.bo.notBefore = time.Now().Add(time.Hour) // gate closed

	c.cycle(context.Background(), opReconcile, false)

	if fast, recon := prov.polls(); fast != 0 || recon != 0 {
		t.Error("provider must not be called while backing off")
	}
	if store.logCount() != 0 {
		t.Error("no sync_log row should be written while backing off")
	}
}

func TestCycleCancelledContextIsCleanExit(t *testing.T) {
	store := &fakeStore{}
	prov := &fakeProvider{}
	c := newTestConn(store, prov, &recordNotifier{})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c.cycle(ctx, opReconcile, false)

	if store.logCount() != 0 {
		t.Error("shutdown must not write a sync_log row (§9)")
	}
	if fast, recon := prov.polls(); fast != 0 || recon != 0 {
		t.Error("provider must not be called on a cancelled context")
	}
	if c.bo.streak != 0 {
		t.Error("shutdown must not apply backoff")
	}
}

// TestCycleCancelDuringCall covers a cancel that races in after the top check
// but before the provider returns: the error is swallowed as a clean exit.
func TestCycleCancelDuringCall(t *testing.T) {
	store := &fakeStore{}
	ctx, cancel := context.WithCancel(context.Background())
	prov := &fakeProvider{
		pollErr: context.Canceled,
		hook:    cancel, // cancel from inside the provider call
	}
	c := newTestConn(store, prov, &recordNotifier{})

	c.cycle(ctx, opReconcile, false)

	if store.logCount() != 0 {
		t.Error("a cancel during the provider call is a clean exit, not a failure")
	}
	if c.bo.streak != 0 {
		t.Error("no backoff on shutdown")
	}
}

// --- identity --------------------------------------------------------------

func TestResolveIdentityClassification(t *testing.T) {
	tests := []struct {
		name          string
		err           error
		wantPermanent bool
	}{
		{"unauthorized is permanent", sdk.ErrUnauthorized, true},
		{"manual-identity-required is permanent", sdk.ErrManualIdentityRequired, true},
		{"network is transient", errors.New("dial tcp: timeout"), false},
		{"wrapped unauthorized is permanent", fmt.Errorf("resolve: %w", sdk.ErrUnauthorized), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prov := &fakeProvider{identityErr: tt.err}
			c := newTestConn(&fakeStore{}, prov, &recordNotifier{})
			c.resolved = false

			if err := c.resolveIdentity(context.Background()); err == nil {
				t.Fatal("expected an error")
			}
			if c.identityPermanent != tt.wantPermanent {
				t.Errorf("identityPermanent = %v, want %v", c.identityPermanent, tt.wantPermanent)
			}
			if c.resolved {
				t.Error("resolved should stay false on identity failure")
			}
		})
	}
}

func TestResolveIdentitySuccess(t *testing.T) {
	prov := &fakeProvider{identity: sdk.Identity{Handle: "octocat"}}
	c := newTestConn(&fakeStore{}, prov, &recordNotifier{})
	c.resolved = false

	if err := c.resolveIdentity(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !c.resolved {
		t.Error("resolved should be true after success")
	}
	if c.identityPermanent {
		t.Error("identityPermanent should be false after success")
	}
}

// TestCycleReResolvesTransientIdentity: a cycle entered with resolved=false
// re-attempts identity at its top and, on success, proceeds to a normal cycle.
func TestCycleReResolvesTransientIdentity(t *testing.T) {
	store := &fakeStore{inserted: 1}
	prov := &fakeProvider{
		identity: sdk.Identity{Handle: "octocat"},
		result:   sdk.PollResult{Events: []sdk.Event{{NativeID: "x"}}},
	}
	c := newTestConn(store, prov, &recordNotifier{})
	c.resolved = false // transient failure earlier

	c.cycle(context.Background(), opReconcile, false)

	if !c.resolved {
		t.Error("identity should be resolved after a successful in-loop retry")
	}
	if store.lastLog(t).Outcome != outcomeOK {
		t.Error("cycle should complete normally once identity resolves")
	}
}

// TestCycleIdentityStillFailing: resolved=false and identity still failing
// classifies the identity error like any other cycle failure.
func TestCycleIdentityStillFailing(t *testing.T) {
	store := &fakeStore{}
	prov := &fakeProvider{identityErr: errors.New("dial tcp: timeout")}
	c := newTestConn(store, prov, &recordNotifier{})
	c.resolved = false

	c.cycle(context.Background(), opReconcile, false)

	if store.lastLog(t).Outcome != outcomeNetwork {
		t.Error("a transient identity failure inside cycle classifies as network")
	}
	if fast, recon := prov.polls(); fast != 0 || recon != 0 {
		t.Error("provider poll must not run until identity resolves")
	}
}

// --- Engine.Run lifecycle --------------------------------------------------

func TestEngineRunLifecycle(t *testing.T) {
	const typ = "fake_run_test"
	prov := &fakeProvider{caps: sdk.Capabilities{FastSignal: true}}

	seeded := make(chan struct{})
	var once sync.Once
	prov.hook = func() { once.Do(func() { close(seeded) }) }

	sdk.Registry[typ] = func(sdk.ConnectionConfig) (sdk.Provider, error) { return prov, nil }
	t.Cleanup(func() { delete(sdk.Registry, typ) })

	store := &fakeStore{}
	notify := &recordNotifier{}
	eng := New(store, []sdk.ConnectionConfig{{ID: "c1", Type: typ}},
		WithNotifier(notify),
		WithIntervals(20*time.Millisecond, 20*time.Millisecond),
		WithCloseTimeout(time.Second),
	)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- eng.Run(ctx) }()

	select {
	case <-seeded:
	case <-time.After(2 * time.Second):
		t.Fatal("engine never ran the seed reconcile")
	}

	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("Run returned %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after cancel")
	}

	prov.mu.Lock()
	closed := prov.closed
	prov.mu.Unlock()
	if !closed {
		t.Error("provider should be Closed on shutdown")
	}
	if store.logCount() == 0 {
		t.Error("at least the seed reconcile should have logged")
	}
}

// TestEngineRunUnknownType logs a startup failure and does not start a loop.
func TestEngineRunUnknownType(t *testing.T) {
	store := &fakeStore{}
	eng := New(store, []sdk.ConnectionConfig{{ID: "bad", Type: "nope"}})

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // no live connections, so Run returns as soon as it observes Done
	if err := eng.Run(ctx); !errors.Is(err, context.Canceled) {
		t.Errorf("Run returned %v, want context.Canceled", err)
	}

	log := store.lastLog(t)
	if log.Outcome != outcomeUnexpected || log.Operation != "startup" {
		t.Errorf("startup log = {%s, %s}, want {unexpected, startup}", log.Operation, log.Outcome)
	}
}

// TestEngineRunPermanentIdentity: a bad token is logged and the connection is
// skipped (dead until restart), its provider Closed.
func TestEngineRunPermanentIdentity(t *testing.T) {
	const typ = "fake_perm_test"
	prov := &fakeProvider{identityErr: sdk.ErrUnauthorized}
	sdk.Registry[typ] = func(sdk.ConnectionConfig) (sdk.Provider, error) { return prov, nil }
	t.Cleanup(func() { delete(sdk.Registry, typ) })

	store := &fakeStore{}
	eng := New(store, []sdk.ConnectionConfig{{ID: "c1", Type: typ}}, WithCloseTimeout(time.Second))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := eng.Run(ctx); !errors.Is(err, context.Canceled) {
		t.Errorf("Run returned %v, want context.Canceled", err)
	}

	if store.lastLog(t).Outcome != outcomeAuth {
		t.Error("permanent identity failure should log an auth startup row")
	}
	prov.mu.Lock()
	closed := prov.closed
	prov.mu.Unlock()
	if !closed {
		t.Error("provider should be Closed when a permanent-identity connection is skipped")
	}
}
