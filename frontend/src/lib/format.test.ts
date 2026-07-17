import { describe, it, expect, beforeEach, afterEach, vi } from "vitest";
import { relativeTime, visibleStates } from "./format";
import type { State } from "./types";

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
