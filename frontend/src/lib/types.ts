// Wire shapes for the DevPit REST API. These mirror the Go structs in
// internal/api (attention.go, connections.go, synclog.go) — that code is
// authoritative (docs/REST_API.md). Keep field names in sync with the JSON tags.

// Nine provider signals in precedence order (internal/attention/states.go,
// ADR/ADR-0016_Presentation_And_Ranking.md). states[0] is the leading chip
// (precedence orders chips, not item ranking — items rank by age band + recency).
// An authored MR is never bare; worst case ["checking"] (gate unknown).
export type State =
  | "changes_requested"
  | "review_requested"
  | "blocked"
  | "mentioned"
  | "ready_to_merge"
  | "auto_merge_armed"
  | "checks_running"
  | "checking"
  | "review_submitted";

// A bucket filter is either a provider signal state or the client-side "mine"
// axis (author is you). "mine" is not a State — authorship is derived from the
// connection identity (config), not the event log, so it lives only client-side.
export type Filter = State | "mine";

// Canonical highest-first precedence, matching internal/attention/states.go.
// Used for bucket ordering and as a client-side sort fallback.
export const STATE_PRECEDENCE: State[] = [
  "changes_requested",
  "review_requested",
  "blocked",
  "mentioned",
  "ready_to_merge",
  "auto_merge_armed",
  "checks_running",
  "checking",
  "review_submitted",
];

export interface AttentionItem {
  id: string;
  connection_id: string;
  connection_label: string;
  connection_type: string;
  object_type: string;
  native_id: string;
  title: string;
  url: string;
  repo: string;
  author: string;
  draft: boolean;
  states: State[];
  muted?: boolean; // reviewed-done: nothing left for me; de-emphasized only (does not affect ranking)
  flagged: boolean;
  stale: boolean;
  old: boolean;
  updated_at: string; // RFC 3339 UTC
  signal_counts?: Record<string, number>; // present only for counts > 1
  auto_merge_armed: boolean;
  checks_running: boolean;
  failing_checks: boolean;
  merge_conflict: boolean;
  needs_rebase: boolean;
  needs_approval: boolean;
  unresolved_discussions: boolean;
  policy_denied: boolean;
  approvals_count: number; // -1=unknown, 0=hide, N=show "N approved"
  my_review_state?: string; // "approved" | "changes_requested" | "reviewed" | ""
  my_roles?: string[]; // your roles on the item: "author" | "reviewer" | "assignee"
  gate_detail?: string;
  flagged_at?: string; // RFC 3339 UTC; present only when pinned
  since?: Record<string, string>; // tag key → RFC 3339 onset time; active tags only
  labels?: string[]; // provider label names (GitLab MR / GitHub PR)
  jira?: { key: string; status: string; url: string };
}

export interface AttentionResponse {
  items: AttentionItem[];
}

export type HealthStatus = "ok" | "degraded" | "failing";

export interface HealthInfo {
  status: HealthStatus;
  last_synced_at: string | null;
  failure_count: number;
  failure_window_minutes: number;
}

export interface Connection {
  id: string;
  type: string;
  base_url: string;
  label: string;
  identity: string | null; // null while pending/failed
  health: HealthInfo;
}

export interface ConnectionsResponse {
  connections: Connection[];
}

export type SyncOperation = "fast_poll" | "reconcile";

export interface SyncLogEntry {
  id: number;
  connection_id: string;
  connection_label: string;
  ts: string;
  operation: SyncOperation;
  outcome: string;
  items_changed: number;
  rate_remaining: number | null;
  retries: number;
  next_retry: string | null;
  error: string | null;
}

export interface SyncLogResponse {
  entries: SyncLogEntry[];
}

// SSE event names (docs/REST_API.md, internal/api/events.go). Coarse by design:
// each says only *that* something changed, so the client re-fetches.
export type SseEventName = "attention.changed" | "sync.completed" | "sync.failed";

export interface ConnEventPayload {
  connection_id: string;
  cause?: string; // present on sync.failed — plain-language banner text
}

export interface ApiError {
  error: "not_found" | "bad_request" | "internal";
  message: string;
}
