# Provider SDK

Providers implement authentication, discovery, synchronization,
normalization, capabilities, and health checks.

Stable interfaces isolate provider-specific logic. One `Provider` instance
is created per configured connection; the engine manages its lifecycle.

## Types

```go
// ConnectionConfig is the resolved, validated config for one connection
// (parsed from the config file at startup).
type ConnectionConfig struct {
    ID      string // opaque, stable; matches connection_id in the DB
    Type    string // "github" | "gitlab"
    BaseURL string // e.g. "https://github.com"; pre-filled for hosted
    Token   string // plaintext PAT (§15)
    Label   string // user-visible: "Personal", "Acme"
}

// Identity is the resolved authenticated user for a connection.
type Identity struct {
    Handle      string // provider username / handle; stored in config after resolve
    DisplayName string // optional; for UI only
}

// Capabilities declares which buckets and optimisations this provider
// can feed given its token type and API version. Absent = cannot say.
// The engine never asks a provider to produce what it declared false for.
type Capabilities struct {
    FastSignal       bool // cheap change-signal tier (notifications/todos)
    MergeGate        bool // can determine Blocked / Ready to Merge
    ChangesRequested bool // can determine Changes Requested bucket
    ConditionalReqs  bool // ETag / If-Modified-Since (GitHub only)
}

// PollState is opaque cursor state carried across poll cycles.
// The engine persists it in sync_cursors; providers own the key names.
// Value type is string to survive round-trips through SQLite TEXT.
type PollState map[string]string

// Event is one normalized event to be written to the event log (§4 schema).
// connection_id and observed_at are stamped by the engine, not the plugin.
type Event struct {
    ObjectType string     // "merge_request" | "issue"
    NativeID   string     // plugin-defined stable ID (e.g. "owner/repo#42")
    EventType  string     // taxonomy from Event_Taxonomy_and_Storage.md
    OccurredAt *time.Time // provider-reported time; nil if unknown
    Actor      string     // provider handle; empty if unknown
    DedupeKey  string     // per §5 of Event_Taxonomy_and_Storage.md
    Payload    any        // typed per EventType; engine serialises to JSON
}

// PollResult is returned by FastPoll and Reconcile.
type PollResult struct {
    Events        []Event
    State         PollState // updated cursors; engine writes to sync_cursors
    RateRemaining *int      // from provider rate-limit headers; nil if unknown
    ItemsChanged  int       // for sync_log
}
```

## Sentinel errors

The engine maps these to the `outcome` column of sync_log (§16) and
displays a plain-language cause in the sync activity view (§12).

```go
var (
    ErrUnauthorized           = errors.New("provider: unauthorized")
    ErrRateLimited            = errors.New("provider: rate limited")
    ErrManualIdentityRequired = errors.New("provider: cannot resolve identity from token")
)
```

Any other error is classified as `network` or `parse` by the engine based
on type (net.Error, json.SyntaxError, etc.).

## Provider interface

```go
// Provider is the interface each plugin must implement.
// One instance per connection; the engine calls each method sequentially
// on the connection's goroutine — no concurrency within a single instance.
type Provider interface {
    // ResolveIdentity fetches the authenticated user's handle.
    // Called once on startup and on token change. Returns
    // ErrManualIdentityRequired when the token cannot resolve a user
    // (bot/deploy tokens — the engine then requires the config to supply
    // a handle manually, per §7).
    ResolveIdentity(ctx context.Context) (Identity, error)

    // Capabilities declares which buckets and optimisations are available.
    // Called once after ResolveIdentity; result is stable for the
    // lifetime of the connection.
    Capabilities() Capabilities

    // FastPoll runs the lightweight change-signal tier (~60 s cadence).
    // state is nil on the first call; the engine passes back the State
    // from the previous result. An empty result is valid (nothing changed).
    FastPoll(ctx context.Context, state PollState) (PollResult, error)

    // Reconcile runs the full identity-scoped sweep (~15 min cadence).
    // Self-heals anything the fast tier may have missed. Same state
    // contract as FastPoll; the two tiers share one PollState map,
    // with namespaced keys to avoid collisions (e.g. "fast.last_modified",
    // "rec.mr_updated_after").
    Reconcile(ctx context.Context, state PollState) (PollResult, error)

    // Close releases any resources held by the provider (HTTP client,
    // open connections). Called when the connection is removed or the
    // engine shuts down.
    Close(ctx context.Context) error
}
```

## Factory & registry

```go
// ProviderFactory constructs a Provider for the given connection.
// Called by the engine once per connection on startup (and on config reload).
type ProviderFactory func(cfg ConnectionConfig) (Provider, error)

// Registry maps provider type strings to factories.
// Plugins self-register at init time; built-in providers register here.
var Registry = map[string]ProviderFactory{}
```

## Payload shapes (per event type)

The `Event.Payload` field carries a typed struct that the engine serialises
to JSON before writing to the `events` table. Shapes follow the fact-set
and signal definitions in `Event_Taxonomy_and_Storage.md §1–2`.

```go
// ItemObservedPayload — for event_type "item.observed" (§2 fact set).
type ItemObservedPayload struct {
    Title            string   `json:"title"`
    URL              string   `json:"url"`
    Repo             string   `json:"repo"`
    State            string   `json:"state"`    // open | merged | closed
    Draft            bool     `json:"draft"`
    Author           string   `json:"author"`
    MyRoles          []string `json:"my_roles"` // author | reviewer | assignee | mentioned
    ReviewDecision   string   `json:"review_decision,omitempty"`
    MyReviewState    string   `json:"my_review_state,omitempty"`
    Gate             string   `json:"gate"`     // ready | blocked | unknown
    GateDetail       string   `json:"gate_detail,omitempty"`
    FailingChecks    bool     `json:"failing_checks"`
    ProviderUpdatedAt string  `json:"provider_updated_at"`
}

// Signal payloads carry the highlights listed in Event_Taxonomy_and_Storage.md §1.
// One struct per signal type; the engine serialises whichever is set.
type SignalMentionedPayload   struct { Direct bool   `json:"direct"` }
type SignalReviewRequestedPayload struct { Direct bool `json:"direct"` }
type SignalReviewSubmittedPayload struct {
    Verdict  string `json:"verdict"` // approved | changes_requested | commented
    Reviewer string `json:"reviewer"`
}
type SignalAssignedPayload  struct { Assigner string `json:"assigner,omitempty"` }
type SignalCIFailedPayload  struct { CheckName string `json:"check_name,omitempty"` }
```

`item.removed` carries no payload (`Payload: nil`).

## Engine responsibilities (not in the interface)

The engine owns:
- Stamping `connection_id` and `observed_at` on every event.
- Writing events via `INSERT OR IGNORE` (dedupe on the `UNIQUE` constraint).
- Persisting `PollResult.State` to `sync_cursors`.
- Writing one `sync_log` cycle row per `FastPoll` / `Reconcile` call.
- Honoring `Retry-After` on `ErrRateLimited`; exponential backoff on
  transient errors.
- Calling `FastPoll` only when `Capabilities().FastSignal` is true (otherwise
  the fast tier runs `Reconcile` at the fast cadence).

## Decisions

- **Shared `PollState` map** for both tiers; plugins namespace their keys
  (e.g. `"fast.last_modified"`, `"rec.mr_updated_after"`). One
  `sync_cursors` row-set per connection, loaded and saved once per cycle.
- **`Close` included** — HTTP connection pools and any ticker goroutines
  need explicit teardown; providers do not rely on GC.
- **`Payload` is `any`** — plugins return typed structs; the engine owns
  JSON serialisation before writing to SQLite. Type safety lives in the
  payload struct definitions above; no round-trip ambiguity.
