import type { State, Filter, AttentionItem, Connection } from "./types";
import { STATE_PRECEDENCE } from "./types";

export interface Bucket {
  state: State;
  label: string;
}

// The set of accepted bucket filters: every signal state plus the client-side
// "mine" axis. Used to validate untrusted input (e.g. the ?bucket= URL param)
// before it reaches matchesFilter, which would otherwise treat unknown junk as a
// state and silently render "no items match".
const FILTERS = new Set<Filter>([...STATE_PRECEDENCE, "mine"]);

// parseFilter validates an arbitrary string (typically the ?bucket= query value)
// against the known Filter set, returning null for anything unrecognized.
export function parseFilter(value: string | null): Filter | null {
  return value && FILTERS.has(value as Filter) ? (value as Filter) : null;
}

// Filter buckets in precedence order (ADR-0016 / internal/attention/states.go).
// checks_running and checking are intentionally absent: they stay valid states
// (still rendered as per-item tags) but don't get their own filter chip.
export const BUCKETS: Bucket[] = [
  { state: "changes_requested", label: "Changes Requested" },
  { state: "review_requested", label: "Review Requested" },
  { state: "blocked", label: "Blocked" },
  { state: "mentioned", label: "Mentioned" },
  { state: "ready_to_merge", label: "Ready to Merge" },
  { state: "auto_merge_armed", label: "Auto-merge Armed" },
  { state: "review_submitted", label: "Review Submitted" },
];

// isMine reports whether the item was authored by you — the connection's own
// identity matches the item author. Identity lives in connection config (not the
// event log), so authorship is a client-side derivation. This is the single
// source of truth for both the "mine" tint (WorkItemRow) and ?bucket=mine.
export function isMine(
  item: AttentionItem,
  connections: Connection[],
): boolean {
  if (!item.author) return false;
  return (
    connections.find((c) => c.id === item.connection_id)?.identity ===
    item.author
  );
}

// isReviewer reports whether you're a reviewer on the item. my_roles is the
// authoritative signal (it carries "reviewer" even before you've reviewed, when
// my_review_state is still empty — the requested-but-not-reviewed case). The
// my_review_state fallback covers providers that drop you from the reviewer list
// once you've reviewed (e.g. GitHub RequestedReviewers) but still report a state.
export function isReviewer(item: AttentionItem): boolean {
  return (
    (item.my_roles?.includes("reviewer") ?? false) || !!item.my_review_state
  );
}

// matchesFilter reports whether an item belongs under the active filter: null
// means "All", "mine" is the authorship axis (needs connections to resolve your
// identity), "mentioned" also gathers your review plate, anything else is a
// plain signal-state match. connections is only consulted for "mine".
export function matchesFilter(
  item: AttentionItem,
  filter: Filter | null,
  connections: Connection[] = [],
): boolean {
  if (!filter) return true;
  if (filter === "mine") return isMine(item, connections);
  if (filter === "mentioned")
    return item.states.includes("mentioned") || isReviewer(item);
  return item.states.includes(filter);
}

export interface VisibleBucket {
  key: Filter;
  label: string;
  count: number;
}

// visibleBuckets is the ordered chip list for the filter bar and the "/" cycle:
// "Mine" first (when you have authored items), then each signal bucket that has
// items, empty ones omitted so the bar stays uncluttered. Counts use
// matchesFilter so each badge matches what the bucket shows (e.g. "mentioned"
// folds in your review plate). Pinned items sit outside the ranked list.
export function visibleBuckets(
  items: AttentionItem[],
  connections: Connection[],
): VisibleBucket[] {
  const active = items.filter((i) => !i.flagged);
  const result: VisibleBucket[] = [];
  const mineCount = active.filter((i) => isMine(i, connections)).length;
  if (mineCount > 0)
    result.push({ key: "mine", label: "Mine", count: mineCount });
  for (const b of BUCKETS) {
    const count = active.filter((i) =>
      matchesFilter(i, b.state, connections),
    ).length;
    if (count > 0) result.push({ key: b.state, label: b.label, count });
  }
  return result;
}

// partitionVisible splits items into the two groups the list renders: pinned
// (flag order, never filtered) then ranked (unflagged and matching the active
// filter). This is the single source of truth for *what is visible*, so the
// renderer (AttentionList) and the keyboard navigation (App) can't disagree
// about which rows exist or their order.
export function partitionVisible(
  items: AttentionItem[],
  filter: Filter | null,
  connections: Connection[],
): { pinned: AttentionItem[]; ranked: AttentionItem[] } {
  const pinned = items.filter((i) => i.flagged);
  const ranked = items.filter(
    (i) => !i.flagged && matchesFilter(i, filter, connections),
  );
  return { pinned, ranked };
}

// visibleOrder is the flat pinned-then-ranked sequence keyboard nav steps
// through — the same rows the renderer shows, in the same order.
export function visibleOrder(
  items: AttentionItem[],
  filter: Filter | null,
  connections: Connection[],
): AttentionItem[] {
  const { pinned, ranked } = partitionVisible(items, filter, connections);
  return [...pinned, ...ranked];
}
