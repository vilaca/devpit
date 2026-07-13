import type { State } from "./types";

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
  if (diff < 86400 * 30) return plural(Math.floor(diff / 86400), "day") + " ago";
  if (diff < 86400 * 365) return plural(Math.floor(diff / (86400 * 30)), "month") + " ago";
  return plural(Math.floor(diff / (86400 * 365)), "year") + " ago";
}

function plural(n: number, unit: string): string {
  return `${n} ${unit}${n === 1 ? "" : "s"}`;
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
