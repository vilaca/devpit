// Package sdk defines the provider contract: the interface a provider (GitHub,
// GitLab, …) implements and the event and payload types it exchanges with the
// sync engine. It is the public boundary between the engine and providers.
package sdk

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// ConnectionConfig is the resolved, validated config for one connection.
type ConnectionConfig struct {
	ID      string // opaque stable ID; matches connection_id in the DB
	Type    string // "github" | "gitlab" | "forgejo" | "gitea" | "codeberg"
	BaseURL string // e.g. "https://github.com"; pre-filled for hosted
	Token   string // plaintext PAT
	Label   string // user-visible: "Personal", "Acme"
	Handle  string // manual identity override for bot/deploy tokens
}

// Identity is the resolved authenticated user for a connection.
type Identity struct {
	Handle      string // provider username / handle; stored in config after resolve
	DisplayName string // optional; for UI only
}

// Capabilities declares which buckets and optimisations a provider supports.
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

// Event is one normalized event to be written to the event log.
// connection_id and observed_at are stamped by the engine, not the plugin.
type Event struct {
	ObjectType string     // "merge_request" | "issue"
	NativeID   string     // plugin-defined stable ID (e.g. "owner/repo#42")
	EventType  string     // taxonomy from Event_Taxonomy_and_Storage.md
	OccurredAt *time.Time // provider-reported time; nil if unknown
	Actor      string     // provider handle; empty if unknown
	DedupeKey  string     // per the dedupe-key rules in Event_Taxonomy_and_Storage.md
	Payload    any        // typed per EventType; engine serialises to JSON
}

// PollResult is returned by FastPoll and Reconcile.
type PollResult struct {
	Events        []Event
	State         PollState // updated cursors; engine writes to sync_cursors
	RateRemaining *int      // from provider rate-limit headers; nil if unknown
	ItemsChanged  int       // for sync_log
}

// Sentinel errors mapped by the engine to sync_log outcome and plain-language
// causes in the sync activity view.
var (
	ErrUnauthorized           = errors.New("provider: unauthorized")
	ErrRateLimited            = errors.New("provider: rate limited")
	ErrServer                 = errors.New("provider: server error")
	ErrParse                  = errors.New("provider: parse error")
	ErrUnexpected             = errors.New("provider: unexpected status")
	ErrManualIdentityRequired = errors.New("provider: cannot resolve identity from token")
)

// RateLimitError wraps ErrRateLimited and carries the provider's retry hint so
// the engine can set delay = max(backoffStreak, RetryAfter).
type RateLimitError struct {
	RetryAfter time.Duration
}

func (e *RateLimitError) Error() string { return ErrRateLimited.Error() }
func (e *RateLimitError) Unwrap() error { return ErrRateLimited }

// StatusError wraps ErrServer (5xx) or ErrUnexpected (other non-success) and
// carries the HTTP status code so the engine can log it without parsing strings.
type StatusError struct {
	Status int
}

func (e *StatusError) Error() string { return fmt.Sprintf("provider: status %d", e.Status) }

func (e *StatusError) Unwrap() error {
	if e.Status >= 500 {
		return ErrServer
	}
	return ErrUnexpected
}

// Provider is the interface each plugin must implement.
// One instance per connection; the engine calls each method sequentially
// on the connection's goroutine — no concurrency within a single instance.
//
// Error contract: when FastPoll or Reconcile returns a non-nil error the engine
// discards the returned PollResult entirely and leaves cursors untouched.
// Providers need not accumulate partial Events or State alongside an error.
type Provider interface {
	// ResolveIdentity fetches the authenticated user's handle.
	// Called once on startup and on token change.
	// Returns ErrManualIdentityRequired when the token cannot resolve a user
	// (bot/deploy tokens — the engine then requires the config to supply a
	// handle manually).
	ResolveIdentity(ctx context.Context) (Identity, error)

	// Capabilities declares which buckets and optimisations are available.
	// Called once after ResolveIdentity; result is stable for the lifetime
	// of the connection.
	Capabilities() Capabilities

	// FastPoll runs the lightweight change-signal tier (~60 s cadence).
	// state is nil on the first call; the engine passes back the State from
	// the previous result. An empty result is valid (nothing changed).
	FastPoll(ctx context.Context, state PollState) (PollResult, error)

	// Reconcile runs the full identity-scoped sweep (~15 min cadence).
	// Self-heals anything the fast tier may have missed. Same state contract
	// as FastPoll; the two tiers share one PollState map with namespaced keys
	// (e.g. "fast.last_modified", "rec.mr_updated_after").
	Reconcile(ctx context.Context, state PollState) (PollResult, error)

	// Close releases any resources held by the provider (HTTP client, open
	// connections). Called when the connection is removed or the engine shuts down.
	Close(ctx context.Context) error
}

// ProviderFactory constructs a Provider for the given connection.
// Called by the engine once per connection on startup (and on config reload).
type ProviderFactory func(cfg ConnectionConfig) (Provider, error)

// Registry maps provider type strings to factories.
// Built-in providers register at init time.
var Registry = map[string]ProviderFactory{}

// Payload shapes — one struct per event type; the engine serialises whichever
// is set. Shapes follow the event model in Event_Taxonomy_and_Storage.md.

// ItemObservedPayload is the payload for event_type "item.observed".
type ItemObservedPayload struct {
	Title                 string   `json:"title"`
	URL                   string   `json:"url"`
	Repo                  string   `json:"repo"`
	State                 string   `json:"state"` // open | merged | closed
	Draft                 bool     `json:"draft"`
	Author                string   `json:"author"`
	MyRoles               []string `json:"my_roles"` // author | reviewer | assignee | mentioned
	ReviewDecision        string   `json:"review_decision,omitempty"`
	MyReviewState         string   `json:"my_review_state,omitempty"`
	Gate                  string   `json:"gate"` // ready | blocked | unknown
	GateDetail            string   `json:"gate_detail,omitempty"`
	FailingChecks         bool     `json:"failing_checks"`
	MergeConflict         bool     `json:"merge_conflict"`
	NeedsRebase           bool     `json:"needs_rebase"`
	NeedsApproval         bool     `json:"needs_approval"`
	UnresolvedDiscussions bool     `json:"unresolved_discussions"`
	PolicyDenied          bool     `json:"policy_denied"`
	AutoMergeArmed        bool     `json:"auto_merge_armed"`
	ChecksRunning         bool     `json:"checks_running"`
	ApprovalsCount        int      `json:"approvals_count,omitempty"` // -1=unknown, 0=none/hide, N=show
	ProviderUpdatedAt     string   `json:"provider_updated_at"`
}

// item.removed carries no payload (Payload: nil).

// SignalMentionedPayload is the payload for event_type "signal.mentioned".
type SignalMentionedPayload struct {
	Direct bool `json:"direct"`
}

// SignalReviewRequestedPayload is the payload for event_type "signal.review_requested".
type SignalReviewRequestedPayload struct {
	Direct bool `json:"direct"`
}

// SignalReviewSubmittedPayload is the payload for event_type "signal.review_submitted".
type SignalReviewSubmittedPayload struct {
	Verdict  string `json:"verdict"` // approved | changes_requested | commented
	Reviewer string `json:"reviewer"`
}

// SignalAssignedPayload is the payload for event_type "signal.assigned".
type SignalAssignedPayload struct {
	Assigner string `json:"assigner,omitempty"`
}

// SignalCIFailedPayload is the payload for event_type "signal.ci_failed".
type SignalCIFailedPayload struct {
	CheckName string `json:"check_name,omitempty"`
}
