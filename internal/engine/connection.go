package engine

import (
	"context"
	"errors"
	"time"

	"github.com/vilaca/devpit/sdk"
)

// conn is the per-connection two-tier scheduler. One goroutine owns it;
// all its mutable state (backoff, resolved) is goroutine-local, so it needs no
// lock. A single select over both tickers serialises FastPoll and Reconcile for
// the same provider instance, satisfying the SDK's "no concurrency within a
// single instance" contract without any per-provider lock.
type conn struct {
	cfg  sdk.ConnectionConfig
	prov sdk.Provider
	caps sdk.Capabilities

	store  Store
	notify Notifier

	fastEvery, reconEvery, closeTimeout time.Duration

	// Goroutine-local mutable state — touched only by this connection's run loop.
	bo                backoff
	resolved          bool // identity resolved; a cycle may run
	identityPermanent bool // dead until config fix + restart (bad token / no handle)
}

// resolveIdentity resolves and classifies the connection's identity. The
// provider owns the manual-handle fallback and returns
// ErrManualIdentityRequired only when both /user and cfg.Handle fail. Bad token
// (ErrUnauthorized) and no-handle (ErrManualIdentityRequired) are permanent;
// anything else (network/server/rate) is transient and retried in-loop.
func (c *conn) resolveIdentity(ctx context.Context) error {
	if _, err := c.prov.ResolveIdentity(ctx); err != nil {
		if errors.Is(err, sdk.ErrManualIdentityRequired) || errors.Is(err, sdk.ErrUnauthorized) {
			c.identityPermanent = true
		}
		c.resolved = false
		return err
	}
	c.resolved = true
	return nil
}

// run drives the connection until ctx is cancelled. It seeds with one
// reconcile so the first render isn't empty while waiting ~15 min for the slow
// tier, then loops on both tickers. Go tickers drop ticks when the receiver is
// busy, so a cycle that overruns its interval simply skips the missed tick —
// no overlap, no queue buildup.
func (c *conn) run(ctx context.Context) {
	fast := time.NewTicker(c.fastEvery)
	recon := time.NewTicker(c.reconEvery)
	defer fast.Stop()
	defer recon.Stop()

	c.cycle(ctx, opReconcile, true)

	for {
		select {
		case <-ctx.Done():
			c.shutdown()
			return
		case <-fast.C:
			if c.caps.FastSignal {
				c.cycle(ctx, opFastPoll, false)
			}
		case <-recon.C:
			c.cycle(ctx, opReconcile, false)
		}
	}
}

// shutdown closes the provider under a bounded, fresh context so cleanup runs
// even though the root context is already cancelled.
func (c *conn) shutdown() {
	closeCtx, cancel := context.WithTimeout(context.Background(), c.closeTimeout)
	defer cancel()
	_ = c.prov.Close(closeCtx)
}
