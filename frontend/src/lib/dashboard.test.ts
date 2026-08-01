import { describe, it, expect, vi, beforeEach } from "vitest";

// Mock the REST layer so toggleFlag's optimistic-apply / rollback is observable
// without a backend. vi.mock is hoisted above the imports below.
vi.mock("./api", () => ({
  getAttention: vi.fn(),
  getConnections: vi.fn(),
  getSyncLog: vi.fn(),
  setFlag: vi.fn(),
  clearFlag: vi.fn(),
}));

import { dashboard } from "./dashboard.svelte";
import { setFlag, clearFlag } from "./api";
import { makeItem } from "./fixtures";

describe("dashboard.toggleFlag", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("pins optimistically and keeps the flag when setFlag resolves", async () => {
    vi.mocked(setFlag).mockResolvedValueOnce(undefined);
    const item = makeItem({ id: "a", flagged: false });
    await dashboard.toggleFlag(item);
    expect(setFlag).toHaveBeenCalledWith("a");
    expect(item.flagged).toBe(true);
  });

  it("rolls the pin back when setFlag rejects", async () => {
    vi.mocked(setFlag).mockRejectedValueOnce(new Error("nope"));
    const item = makeItem({ id: "b", flagged: false });
    await dashboard.toggleFlag(item);
    expect(item.flagged).toBe(false);
  });

  it("unpins via clearFlag and keeps it cleared on success", async () => {
    vi.mocked(clearFlag).mockResolvedValueOnce(undefined);
    const item = makeItem({ id: "c", flagged: true });
    await dashboard.toggleFlag(item);
    expect(clearFlag).toHaveBeenCalledWith("c");
    expect(item.flagged).toBe(false);
  });

  it("restores the pin when clearFlag rejects", async () => {
    vi.mocked(clearFlag).mockRejectedValueOnce(new Error("nope"));
    const item = makeItem({ id: "d", flagged: true });
    await dashboard.toggleFlag(item);
    expect(item.flagged).toBe(true);
  });
});
