import type { Connection, State } from "./types";

// relativeTime converts an RFC 3339 UTC string to a human-readable relative
// label ("2 hours ago", "3 days ago"). Falls back to the raw string on parse
// failure. Clamped to units ≥ seconds; future dates show "just now".
export function relativeTime(iso: string): string {
  const d = new Date(iso);
  if (isNaN(d.getTime())) return iso;
  const diff = Math.floor((Date.now() - d.getTime()) / 1000);
  if (diff < 60) return "just now";
  if (diff < 3600) return plural(Math.floor(diff / 60), "minute") + " ago";
  if (diff < 86400) return plural(Math.floor(diff / 3600), "hour") + " ago";
  if (diff < 86400 * 30)
    return plural(Math.floor(diff / 86400), "day") + " ago";
  if (diff < 86400 * 365)
    return plural(Math.floor(diff / (86400 * 30)), "month") + " ago";
  return plural(Math.floor(diff / (86400 * 365)), "year") + " ago";
}

function plural(n: number, unit: string): string {
  return `${n} ${unit}${n === 1 ? "" : "s"}`;
}

// emptyListCopy is the empty-attention sentence (ADR-0018): never conflate
// "nothing to do" with "sync is broken". A failing connection replaces
// "All clear"; otherwise we cite the oldest last_synced_at (most conservative).
export function emptyListCopy(connections: Connection[]): string {
  if (connections.length === 0) {
    return "All clear — no connections configured";
  }
  const failing = connections.filter((c) => c.health.status === "failing");
  if (failing.length > 0) {
    const names = failing.map((c) => c.label).join(", ");
    return `Sync failed for ${names} — the list may be incomplete.`;
  }
  const times = connections
    .map((c) => c.health.last_synced_at)
    .filter((t): t is string => t != null);
  if (times.length === 0) {
    return "All clear — synced never";
  }
  let oldest = times[0];
  for (const t of times) {
    if (new Date(t).getTime() < new Date(oldest).getTime()) oldest = t;
  }
  return `All clear — synced ${relativeTime(oldest)}`;
}

// stateLabel maps wire state values to display labels.
export function stateLabel(s: State): string {
  return STATE_LABELS[s] ?? s;
}

const STATE_LABELS: Record<State, string> = {
  changes_requested: "Changes Requested",
  review_requested: "Review Requested",
  blocked: "Blocked",
  mentioned: "Mentioned",
  ready_to_merge: "Ready to Merge",
  auto_merge_armed: "Auto-merge Armed",
  checks_running: "Checks Running",
  checking: "Checking",
  review_submitted: "Review Submitted",
};

// stateCSSVar maps a state to the corresponding CSS token on :root.
export function stateCSSVar(s: State): string {
  return `var(--state-${s.replace(/_/g, "-")})`;
}

// States that still render a chip on a muted (reviewed-done) row. Everything
// else — other state chips plus the draft/marker/stale badges — is suppressed
// when muted (ADR-0016). Today only changes_requested survives: it means "I
// requested changes; ball is with the author", and the dim row keeps this chip
// so it says why it's dim.
const MUTED_VISIBLE: ReadonlySet<State> = new Set<State>(["changes_requested"]);

// visibleStates returns the state chips to render for a row: all of them
// normally, or just the muted-visible subset when the row is muted.
export function visibleStates(states: State[], muted: boolean): State[] {
  return muted ? states.filter((s) => MUTED_VISIBLE.has(s)) : states;
}
