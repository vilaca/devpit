package engine

// Notifier bridges the engine to the SSE hub in internal/api (coarse events —
// docs/REST_API.md). The engine defines it and imports nothing from api; main wires the
// hub in. The default is noopNotifier so the engine runs headless in tests.
type Notifier interface {
	// AttentionChanged is global: it tells the client to re-fetch GET /attention
	// because the folded bucket state may have changed. Fired only when a cycle
	// actually inserted new events.
	AttentionChanged()
	// SyncCompleted is per-connection: a cycle finished cleanly (health dot +
	// sync activity view).
	SyncCompleted(connID string)
	// SyncFailed is per-connection: a cycle failed; cause is plain-language text
	// suitable for the sync activity view.
	SyncFailed(connID, cause string)
}

// noopNotifier is the default Notifier: it drops every event. It lets the
// engine run without an API layer wired in (tests, headless start).
type noopNotifier struct{}

func (noopNotifier) AttentionChanged()         {}
func (noopNotifier) SyncCompleted(string)      {}
func (noopNotifier) SyncFailed(string, string) {}
