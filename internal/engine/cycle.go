package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/vilaca/devpit/internal/storage"
	"github.com/vilaca/devpit/sdk"
)

// Fact-stream event types and the open item state, folded here to reap items
// that left a complete reconcile sweep (docs/Event_Taxonomy_and_Storage.md).
const (
	eventItemObserved = "item.observed"
	eventItemRemoved  = "item.removed"
	itemStateOpen     = "open"
)

// operation identifies which poll tier a cycle is running. Its String is the
// value written to sync_log.operation (docs/Event_Taxonomy_and_Storage.md).
type operation int

const (
	opFastPoll operation = iota
	opReconcile
)

func (o operation) String() string {
	switch o {
	case opFastPoll:
		return "fast_poll"
	case opReconcile:
		return "reconcile"
	default:
		return "unknown"
	}
}

// sync_log outcomes (docs/Synchronization_Engine.md). Free TEXT column, so
// extending the set needs no migration.
const (
	outcomeOK          = "ok"
	outcomeDegraded    = "degraded" // cycle succeeded but enrichment partially failed
	outcomeAuth        = "auth"
	outcomeRateLimited = "rate_limited"
	outcomeNetwork     = "network"
	outcomeServer      = "server"
	outcomeParse       = "parse"
	outcomeStorage     = "storage"
	outcomeUnexpected  = "unexpected"
)

// cycle runs one poll cycle for op. The governing rule: PollResult is
// consumed only on success — on any error the engine persists nothing and
// leaves cursors untouched, so the next cycle re-covers the whole batch from
// the unchanged cursor (INSERT OR IGNORE makes the re-fetch idempotent).
func (c *conn) cycle(ctx context.Context, op operation, startup bool) {
	if ctx.Err() != nil {
		return // shutting down; the select loop will observe Done next
	}
	if !c.bo.ready() {
		return // still backing off
	}
	if !c.resolved { // transient identity failure earlier; retry at the top
		if err := c.resolveIdentity(ctx); err != nil {
			if ctx.Err() != nil {
				return
			}
			c.fail(op, err)
			return
		}
	}

	state, err := c.store.LoadCursors(ctx, c.cfg.ID)
	if err != nil {
		if ctx.Err() != nil {
			return
		}
		c.logStorage(op, 0, err)
		return
	}

	var result sdk.PollResult
	switch op {
	case opFastPoll:
		result, err = c.prov.FastPoll(ctx, state)
	case opReconcile:
		// startup=true passes nil so providers do a full sweep regardless of the
		// stored updated_after cursor, populating openSnapshots for all open items.
		// SaveCursors is upsert-only so the fast_poll cursor in the DB is safe.
		if startup {
			result, err = c.prov.Reconcile(ctx, nil)
		} else {
			result, err = c.prov.Reconcile(ctx, state)
		}
	}
	if err != nil {
		if ctx.Err() != nil {
			return // shutdown, not a failure
		}
		c.fail(op, err)
		return
	}

	c.persist(ctx, op, result)
}

// persist runs the success path for a completed poll: reap (on a complete
// sweep), then events first (durable) then cursors, then the sync_log row and
// notifications. A crash between events and cursors leaves the cursor
// unadvanced, so events are re-fetched, never lost; both writes are no-ops on
// empty input. Any storage failure is logged via logStorage and aborts the rest.
func (c *conn) persist(ctx context.Context, op operation, result sdk.PollResult) {
	// On a complete authoritative sweep, reap items that left it and salt any
	// resurrection so it supersedes a prior removal (ADR-0024). Only Reconcile
	// sets Complete, so FastPoll's partial results never reap. The synthesized
	// removals ride the same durable WriteEvents path below and count toward
	// inserted, so AttentionChanged fires with no extra wiring.
	if result.Complete {
		removals, rerr := c.reap(ctx, result.Events)
		if rerr != nil {
			c.abortStorage(ctx, op, 0, rerr)
			return
		}
		result.Events = append(result.Events, removals...)
	}

	inserted, werr := c.store.WriteEvents(ctx, c.cfg.ID, result.Events)
	if werr != nil {
		c.abortStorage(ctx, op, 0, werr)
		return
	}
	if werr = c.store.SaveCursors(ctx, c.cfg.ID, result.State); werr != nil {
		c.abortStorage(ctx, op, inserted, werr)
		return
	}

	c.bo.reset()
	outcome := outcomeOK
	if result.Degraded {
		outcome = outcomeDegraded
	}
	c.writeLog(storage.SyncLogEntry{
		Ts:            time.Now(),
		ConnectionID:  c.cfg.ID,
		Operation:     op.String(),
		Outcome:       outcome,
		ItemsChanged:  inserted,
		RateRemaining: result.RateRemaining,
	})
	if inserted > 0 {
		c.notify.AttentionChanged()
	}
	c.notify.SyncCompleted(c.cfg.ID)
}

// abortStorage records a local storage failure during the success path, unless
// the context was cancelled (a shutdown, which writes no row).
func (c *conn) abortStorage(ctx context.Context, op operation, inserted int, err error) {
	if ctx.Err() != nil {
		return
	}
	c.logStorage(op, inserted, err)
}

// reap diffs a complete reconcile sweep against the store's latest per-item facts
// and returns item.removed events for the open roled items the sweep no longer
// sees — merged, closed, or access/role lost (ADR-0024). It also salts, in place,
// the dedupe key of any swept snapshot whose latest stored fact is a removal, so
// an item re-observed with an identical fact set still inserts a fresh superseding
// snapshot instead of being dropped by INSERT OR IGNORE. A store read failure is
// returned so the caller can treat it like any storage failure and persist nothing.
func (c *conn) reap(ctx context.Context, events []sdk.Event) ([]sdk.Event, error) {
	facts, err := c.store.LatestItemFacts(ctx, c.cfg.ID)
	if err != nil {
		return nil, err
	}

	// swept = native_ids of the item.observed snapshots in this sweep.
	swept := make(map[string]bool, len(events))
	for _, e := range events {
		if e.EventType == eventItemObserved {
			swept[e.NativeID] = true
		}
	}

	// Index each item's latest stored fact, parsing the observed payload for the
	// engine's own view of open-ness and roles (mention-only items carry no role
	// and are outside reconcile's authority, so they are never reaped).
	type latestFact struct {
		objectType string
		eventID    int64
		removed    bool
		openRoled  bool
	}
	latest := make(map[string]latestFact, len(facts))
	for _, f := range facts {
		lf := latestFact{objectType: f.ObjectType, eventID: f.EventID, removed: f.EventType == eventItemRemoved}
		if f.EventType == eventItemObserved {
			var pl sdk.ItemObservedPayload
			if err := json.Unmarshal(f.Payload, &pl); err != nil {
				// Payloads are engine-written, so this is defensive: a corrupt
				// snapshot leaves openRoled false, making the item a ghost that
				// is never reaped nor resurrected. Log it so the corruption is
				// observable rather than silent. Control flow is unchanged.
				log.Printf("engine: connection %q: reap: unparseable item.observed payload for %q: %v",
					c.cfg.ID, f.NativeID, err)
			} else {
				lf.openRoled = pl.State == itemStateOpen && len(pl.MyRoles) > 0
			}
		}
		latest[f.NativeID] = lf
	}

	// Reap: an open roled item absent from a complete sweep is genuinely gone.
	// Key the removal to the superseded observed event's id — per-episode, so a
	// reopen→re-merge yields a fresh, higher-id removal, and an already-removed
	// item (latest fact is a removal) is skipped, so a still-gone item is not
	// re-removed every cycle.
	var removals []sdk.Event
	for nid, lf := range latest {
		if lf.openRoled && !swept[nid] {
			removals = append(removals, sdk.Event{
				ObjectType: lf.objectType,
				NativeID:   nid,
				EventType:  eventItemRemoved,
				DedupeKey:  fmt.Sprintf("item.removed:%d", lf.eventID),
			})
		}
	}

	// Resurrect: a swept snapshot whose latest stored fact is a removal must
	// supersede it even if the fact set is unchanged — salt its dedupe key with
	// the removal's event id so INSERT OR IGNORE inserts a fresh, higher-id row.
	for i := range events {
		e := &events[i]
		if e.EventType != eventItemObserved {
			continue
		}
		if lf, ok := latest[e.NativeID]; ok && lf.removed {
			e.DedupeKey = fmt.Sprintf("%s:resurrect:%d", e.DedupeKey, lf.eventID)
		}
	}

	return removals, nil
}

// fail classifies a provider error into a sync-log outcome, applies backoff,
// and notifies. It relies on the classified SDK errors (docs/Provider_SDK.md).
func (c *conn) fail(op operation, err error) {
	switch {
	case errors.Is(err, sdk.ErrUnauthorized):
		// Token went bad at runtime: force re-resolution and back off.
		c.resolved = false
		c.bo.bump()
		c.writeLog(storage.SyncLogEntry{
			Ts:           time.Now(),
			ConnectionID: c.cfg.ID,
			Operation:    op.String(),
			Outcome:      outcomeAuth,
			Retries:      c.bo.streak,
			Error:        errText(err),
		})
		c.notify.SyncFailed(c.cfg.ID, "authentication failed — check the token")

	case errors.Is(err, sdk.ErrRateLimited):
		var hint *time.Duration
		var rle *sdk.RateLimitError
		if errors.As(err, &rle) {
			hint = &rle.RetryAfter
		}
		d := c.bo.rateLimit(hint) // floor = max(exponential, hint)
		next := time.Now().Add(d)
		c.writeLog(storage.SyncLogEntry{
			Ts:           time.Now(),
			ConnectionID: c.cfg.ID,
			Operation:    op.String(),
			Outcome:      outcomeRateLimited,
			Retries:      c.bo.streak,
			NextRetry:    &next,
			Error:        errText(err),
		})
		c.notify.SyncFailed(c.cfg.ID, fmt.Sprintf("rate limited — retry in %s", d.Round(time.Second)))

	default:
		outcome, status := classify(err)
		c.bo.bump()
		c.writeLog(storage.SyncLogEntry{
			Ts:           time.Now(),
			ConnectionID: c.cfg.ID,
			Operation:    op.String(),
			Outcome:      outcome,
			HTTPStatus:   status,
			Retries:      c.bo.streak,
			Error:        errText(err),
		})
		c.notify.SyncFailed(c.cfg.ID, c.causeText(outcome, status))
	}
}

// classify maps a non-rate, non-auth provider error to a sync_log outcome and,
// where the SDK carries it, the HTTP status. It reads the status via the typed
// StatusError rather than parsing message strings.
func classify(err error) (outcome string, status *int) {
	var se *sdk.StatusError
	if errors.As(err, &se) {
		s := se.Status
		if s >= 500 {
			return outcomeServer, &s
		}
		return outcomeUnexpected, &s
	}
	switch {
	case errors.Is(err, sdk.ErrServer):
		return outcomeServer, nil
	case errors.Is(err, sdk.ErrUnexpected):
		return outcomeUnexpected, nil
	case errors.Is(err, sdk.ErrParse):
		return outcomeParse, nil
	default:
		// Transport / unclassified: DNS, TCP, TLS, timeout — no HTTP response.
		return outcomeNetwork, nil
	}
}

// causeText renders the plain-language cause shown in the sync activity view.
// auth and rate_limited carry their own copy in fail; this covers the
// classify outcomes.
func (c *conn) causeText(outcome string, status *int) string {
	switch outcome {
	case outcomeServer:
		if status != nil {
			return fmt.Sprintf("%s server error (%d) — will retry", c.cfg.Type, *status)
		}
		return c.cfg.Type + " server error — will retry"
	case outcomeParse:
		return "unexpected response format"
	case outcomeUnexpected:
		if status != nil {
			return fmt.Sprintf("unexpected status %d", *status)
		}
		return "unexpected error"
	case outcomeNetwork:
		return "couldn't reach " + c.cfg.Type
	default:
		return outcome
	}
}

// logStorage records a local storage failure and applies backoff (bump before
// log, so Retries reflects the new streak, matching fail). inserted is carried
// through when WriteEvents succeeded but SaveCursors failed, so the row reflects
// the rows that did land even though the cycle is treated as a failure.
func (c *conn) logStorage(op operation, inserted int, err error) {
	c.bo.bump()
	c.writeLog(storage.SyncLogEntry{
		Ts:           time.Now(),
		ConnectionID: c.cfg.ID,
		Operation:    op.String(),
		Outcome:      outcomeStorage,
		ItemsChanged: inserted,
		Retries:      c.bo.streak,
		Error:        errText(err),
	})
	c.notify.SyncFailed(c.cfg.ID, "local storage error")
}

// writeLog persists one sync_log row. A failure here (storage itself is down)
// cannot be recorded in storage, so it is logged to stderr and dropped rather
// than derailing the cycle.
func (c *conn) writeLog(entry storage.SyncLogEntry) {
	// Use a detached context so a shutdown mid-cycle still records the outcome.
	logCtx, cancel := context.WithTimeout(context.Background(), c.closeTimeout)
	defer cancel()
	if err := c.store.WriteSyncLog(logCtx, entry); err != nil {
		log.Printf("engine: connection %q: write sync_log: %v", c.cfg.ID, err)
	}
}

// errText returns a pointer to err's message, or nil for a nil error, matching
// the nullable sync_log.error column.
func errText(err error) *string {
	if err == nil {
		return nil
	}
	s := err.Error()
	return &s
}
