// Wire shapes for the DevPit REST API. These mirror the Go structs in
// internal/api (attention.go, connections.go, synclog.go) — that code is
// authoritative (docs/REST_API.md). Keep field names in sync with the JSON tags.

// The six attention states, in precedence order (internal/attention/states.go).
// A WorkItem may carry several at once; they render as tags.
export type State =
  | "needs_review"
  | "changes_requested"
  | "blocked"
  | "ready_to_merge"
  | "mentioned"
  | "waiting_on_author";

// Canonical highest-first precedence, matching internal/attention/states.go.
// Used for bucket ordering and as a client-side sort fallback.
export const STATE_PRECEDENCE: State[] = [
  "needs_review",
  "changes_requested",
  "blocked",
  "ready_to_merge",
  "mentioned",
  "waiting_on_author",
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
  flagged: boolean;
  stale: boolean;
  abandoned: boolean;
  updated_at: string; // RFC 3339 UTC
  signal_counts?: Record<string, number>; // present only for counts > 1
  failing_checks: boolean;
  merge_conflict: boolean;
  needs_rebase: boolean;
  gate_detail?: string;
  flagged_at?: string; // RFC 3339 UTC; present only when pinned
  since?: Record<string, string>; // tag key → RFC 3339 onset time; active tags only
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
