// Test fixtures: minimal factories for the wire shapes, so tests spell out only
// the fields under test. Not imported by app code (tree-shaken from the build).

import type { AttentionItem, Connection, HealthStatus } from "./types";

export function makeItem(
  overrides: Partial<AttentionItem> = {},
): AttentionItem {
  return {
    id: "item-1",
    connection_id: "conn-1",
    connection_label: "Conn",
    connection_type: "github",
    object_type: "pr",
    native_id: "1",
    title: "A change",
    url: "https://example.test/1",
    repo: "org/repo",
    author: "someone",
    draft: false,
    states: [],
    flagged: false,
    stale: false,
    old: false,
    updated_at: "2026-07-16T00:00:00Z",
    auto_merge_armed: false,
    checks_running: false,
    failing_checks: false,
    merge_conflict: false,
    needs_rebase: false,
    needs_approval: false,
    unresolved_discussions: false,
    policy_denied: false,
    approvals_count: 0,
    ...overrides,
  };
}

export function makeConnection(
  overrides: Partial<Connection> = {},
): Connection {
  const status: HealthStatus = "ok";
  return {
    id: "conn-1",
    type: "github",
    base_url: "https://example.test",
    label: "Conn",
    identity: "me",
    health: {
      status,
      last_synced_at: "2026-07-16T00:00:00Z",
      failure_count: 0,
      failure_window_minutes: 60,
    },
    ...overrides,
  };
}
