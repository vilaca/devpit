package engine

import "time"

// maxBackoffShift caps the exponential streak at 1<<4 = 16 minutes.
const maxBackoffShift = 4

// backoff is a goroutine-local retry gate (§8). It holds no timers and does no
// rescheduling: the connection's tickers keep firing, and a cycle that finds
// the gate closed simply returns early until notBefore passes. Because it is
// touched only by its own connection's goroutine, it needs no lock.
type backoff struct {
	notBefore time.Time
	streak    int // consecutive failures; 0 when healthy
}

// ready reports whether a new cycle may run now.
func (b *backoff) ready() bool { return !time.Now().Before(b.notBefore) }

// reset clears the gate after a successful cycle.
func (b *backoff) reset() {
	b.streak = 0
	b.notBefore = time.Time{}
}

// bump applies exponential backoff for a transient or auth failure:
// 1 → 2 → 4 → 8 → 16 minutes, then held at 16.
func (b *backoff) bump() {
	b.streak++
	b.notBefore = time.Now().Add(b.delay())
}

// rateLimit sets the gate for a rate-limit failure and returns the chosen wait.
// It never retries before the provider's floor (hint, Q3) but waits longer if
// the exponential streak already demands it: delay = max(exponential, hint).
func (b *backoff) rateLimit(hint *time.Duration) time.Duration {
	b.streak++
	d := b.delay()
	if hint != nil && *hint > d {
		d = *hint
	}
	b.notBefore = time.Now().Add(d)
	return d
}

// delay is the exponential component for the current streak (streak >= 1).
func (b *backoff) delay() time.Duration {
	return time.Minute << min(b.streak-1, maxBackoffShift)
}
