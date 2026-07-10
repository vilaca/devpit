package engine

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/vilaca/devpit/internal/storage"
	"github.com/vilaca/devpit/sdk"
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

	// Success path: events first (durable), then cursors — a crash between them
	// leaves the cursor unadvanced, so events are re-fetched, never lost. Both
	// are no-ops on empty input.
	inserted, werr := c.store.WriteEvents(ctx, c.cfg.ID, result.Events)
	if werr != nil {
		if ctx.Err() != nil {
			return
		}
		c.logStorage(op, 0, werr)
		return
	}
	if werr = c.store.SaveCursors(ctx, c.cfg.ID, result.State); werr != nil {
		if ctx.Err() != nil {
			return
		}
		c.logStorage(op, inserted, werr)
		return
	}

	c.bo.reset()
	c.writeLog(storage.SyncLogEntry{
		Ts:            time.Now(),
		ConnectionID:  c.cfg.ID,
		Operation:     op.String(),
		Outcome:       outcomeOK,
		ItemsChanged:  inserted,
		RateRemaining: result.RateRemaining,
	})
	if inserted > 0 {
		c.notify.AttentionChanged()
	}
	c.notify.SyncCompleted(c.cfg.ID)
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
