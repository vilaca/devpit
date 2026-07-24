import type { State, AttentionItem } from "./types";

export interface Bucket {
  state: State;
  label: string;
}

// Nine buckets in precedence order (ADR-0016 / internal/attention/states.go).
export const BUCKETS: Bucket[] = [
  { state: "changes_requested", label: "Changes Requested" },
  { state: "review_requested", label: "Review Requested" },
  { state: "blocked", label: "Blocked" },
  { state: "mentioned", label: "Mentioned" },
  { state: "ready_to_merge", label: "Ready to Merge" },
  { state: "auto_merge_armed", label: "Auto-merge Armed" },
  { state: "checks_running", label: "Checks Running" },
  { state: "checking", label: "Checking" },
  { state: "review_submitted", label: "Review Submitted" },
];

// countByState returns a map of state → item count (non-flagged items only,
// since pinned items are outside the ranked list and the filter applies there).
export function countByState(items: AttentionItem[]): Map<State, number> {
  const counts = new Map<State, number>();
  for (const item of items) {
    if (item.flagged) continue;
    for (const s of item.states) {
      counts.set(s, (counts.get(s) ?? 0) + 1);
    }
  }
  return counts;
}
