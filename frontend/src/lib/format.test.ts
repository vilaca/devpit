import { describe, it, expect, beforeEach, afterEach, vi } from "vitest";
import { emptyListCopy, relativeTime, visibleStates } from "./format";
import type { State } from "./types";
import { makeConnection } from "./fixtures";

const NOW = new Date("2026-07-16T12:00:00.000Z").getTime();
const ago = (seconds: number): string =>
  new Date(NOW - seconds * 1000).toISOString();

describe("relativeTime", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    vi.setSystemTime(NOW);
  });
  afterEach(() => {
    vi.useRealTimers();
  });

  it("shows 'just now' under a minute", () => {
    expect(relativeTime(ago(30))).toBe("just now");
  });

  it("crosses the minute boundary at 60s", () => {
    expect(relativeTime(ago(60))).toBe("1 minute ago");
    expect(relativeTime(ago(120))).toBe("2 minutes ago");
    expect(relativeTime(ago(3599))).toBe("59 minutes ago");
  });

  it("crosses the hour boundary at 3600s", () => {
    expect(relativeTime(ago(3600))).toBe("1 hour ago");
    expect(relativeTime(ago(86399))).toBe("23 hours ago");
  });

  it("crosses the day boundary at 86400s", () => {
    expect(relativeTime(ago(86400))).toBe("1 day ago");
    expect(relativeTime(ago(86400 * 29))).toBe("29 days ago");
  });

  it("crosses the month boundary at 30 days", () => {
    expect(relativeTime(ago(86400 * 30))).toBe("1 month ago");
  });

  it("crosses the year boundary at 365 days", () => {
    expect(relativeTime(ago(86400 * 365))).toBe("1 year ago");
  });

  it("returns the raw string on a parse failure", () => {
    expect(relativeTime("not-a-date")).toBe("not-a-date");
  });

  it("treats a future date as 'just now'", () => {
    expect(relativeTime(ago(-30))).toBe("just now");
  });
});

describe("visibleStates", () => {
  it("returns all states when not muted", () => {
    const states: State[] = ["blocked", "mentioned", "review_submitted"];
    expect(visibleStates(states, false)).toEqual(states);
  });

  it("keeps only changes_requested when muted", () => {
    const states: State[] = ["changes_requested", "review_submitted"];
    expect(visibleStates(states, true)).toEqual(["changes_requested"]);
  });

  it("hides every chip on a muted approve/comment row", () => {
    // reviewer-side approved/commented rows collapse to review_submitted, which
    // the mute suppresses -> a chipless dim row.
    expect(visibleStates(["review_submitted"], true)).toEqual([]);
  });
});

describe("emptyListCopy", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    vi.setSystemTime(NOW);
  });
  afterEach(() => {
    vi.useRealTimers();
  });

  it("names missing connections instead of pretending a sync ran", () => {
    expect(emptyListCopy([])).toBe("All clear — no connections configured");
  });

  it("cites the oldest last_synced_at when every connection is healthy", () => {
    const newer = makeConnection({
      id: "a",
      label: "GitHub",
      health: { status: "ok", last_synced_at: ago(60) },
    });
    const older = makeConnection({
      id: "b",
      label: "GitLab",
      health: { status: "ok", last_synced_at: ago(3600) },
    });
    expect(emptyListCopy([newer, older])).toBe("All clear — synced 1 hour ago");
  });

  it("says synced never when no connection has a timestamp", () => {
    expect(
      emptyListCopy([
        makeConnection({
          health: { status: "ok", last_synced_at: null },
        }),
      ]),
    ).toBe("All clear — synced never");
  });

  it("replaces All clear when any connection is failing", () => {
    const ok = makeConnection({
      id: "ok",
      label: "GitHub",
      health: { status: "ok", last_synced_at: ago(60) },
    });
    const bad = makeConnection({
      id: "bad",
      label: "GitLab",
      health: { status: "failing", last_synced_at: ago(3600) },
    });
    expect(emptyListCopy([ok, bad])).toBe(
      "Sync failed for GitLab — the list may be incomplete.",
    );
  });

  it("lists every failing connection", () => {
    const a = makeConnection({
      id: "a",
      label: "GitHub",
      health: { status: "failing", last_synced_at: null },
    });
    const b = makeConnection({
      id: "b",
      label: "GitLab",
      health: { status: "failing", last_synced_at: null },
    });
    expect(emptyListCopy([a, b])).toBe(
      "Sync failed for GitHub, GitLab — the list may be incomplete.",
    );
  });

  it("still says All clear when a connection is only degraded", () => {
    expect(
      emptyListCopy([
        makeConnection({
          health: { status: "degraded", last_synced_at: ago(60) },
        }),
      ]),
    ).toBe("All clear — synced 1 minute ago");
  });
});
