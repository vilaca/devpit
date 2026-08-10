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
import {
  setFlag,
  clearFlag,
  getAttention,
  getConnections,
  getSyncLog,
} from "./api";
import { makeConnection, makeItem } from "./fixtures";
import type {
  AttentionResponse,
  ConnectionsResponse,
  SyncLogResponse,
} from "./types";
import { deferred } from "./deferred";

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

describe("dashboard.hydrate", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("keeps the newest snapshot when an earlier hydration finishes last", async () => {
    const firstAttention = deferred<AttentionResponse>();
    const firstConnections = deferred<ConnectionsResponse>();
    const firstSyncLog = deferred<SyncLogResponse>();
    const secondAttention = deferred<AttentionResponse>();
    const secondConnections = deferred<ConnectionsResponse>();
    const secondSyncLog = deferred<SyncLogResponse>();

    vi.mocked(getAttention)
      .mockReturnValueOnce(firstAttention.promise)
      .mockReturnValueOnce(secondAttention.promise);
    vi.mocked(getConnections)
      .mockReturnValueOnce(firstConnections.promise)
      .mockReturnValueOnce(secondConnections.promise);
    vi.mocked(getSyncLog)
      .mockReturnValueOnce(firstSyncLog.promise)
      .mockReturnValueOnce(secondSyncLog.promise);

    const first = dashboard.hydrate();
    const second = dashboard.hydrate();

    secondAttention.resolve({ items: [makeItem({ id: "new" })] });
    secondConnections.resolve({
      connections: [makeConnection({ label: "New connection" })],
      update: { available: false, in_container: false },
    });
    secondSyncLog.resolve({ entries: [] });
    await second;

    firstAttention.resolve({ items: [makeItem({ id: "old" })] });
    firstConnections.resolve({
      connections: [makeConnection({ label: "Old connection" })],
      update: { available: true, in_container: true },
    });
    firstSyncLog.resolve({ entries: [] });
    await first;

    expect(dashboard.items.map((item) => item.id)).toEqual(["new"]);
    expect(dashboard.connections.map((connection) => connection.label)).toEqual(
      ["New connection"],
    );
    expect(dashboard.update).toEqual({ available: false, in_container: false });
  });

  it("ignores an earlier hydration failure after a newer snapshot succeeds", async () => {
    const firstAttention = deferred<AttentionResponse>();
    const firstConnections = deferred<ConnectionsResponse>();
    const firstSyncLog = deferred<SyncLogResponse>();
    const secondAttention = deferred<AttentionResponse>();
    const secondConnections = deferred<ConnectionsResponse>();
    const secondSyncLog = deferred<SyncLogResponse>();

    vi.mocked(getAttention)
      .mockReturnValueOnce(firstAttention.promise)
      .mockReturnValueOnce(secondAttention.promise);
    vi.mocked(getConnections)
      .mockReturnValueOnce(firstConnections.promise)
      .mockReturnValueOnce(secondConnections.promise);
    vi.mocked(getSyncLog)
      .mockReturnValueOnce(firstSyncLog.promise)
      .mockReturnValueOnce(secondSyncLog.promise);

    const first = dashboard.hydrate();
    const second = dashboard.hydrate();

    secondAttention.resolve({ items: [makeItem({ id: "new" })] });
    secondConnections.resolve({
      connections: [makeConnection()],
      update: { available: false, in_container: false },
    });
    secondSyncLog.resolve({ entries: [] });
    await second;

    firstAttention.reject(new Error("old request failed"));
    await first;

    expect(dashboard.items.map((item) => item.id)).toEqual(["new"]);
    expect(dashboard.loadError).toBeNull();
    expect(dashboard.loading).toBe(false);
  });

  it("keeps a newer live attention refresh when hydration finishes later", async () => {
    const hydrationAttention = deferred<AttentionResponse>();
    const hydrationConnections = deferred<ConnectionsResponse>();
    const hydrationSyncLog = deferred<SyncLogResponse>();
    const refreshedAttention = deferred<AttentionResponse>();

    vi.mocked(getAttention)
      .mockReturnValueOnce(hydrationAttention.promise)
      .mockReturnValueOnce(refreshedAttention.promise);
    vi.mocked(getConnections).mockReturnValueOnce(hydrationConnections.promise);
    vi.mocked(getSyncLog).mockReturnValueOnce(hydrationSyncLog.promise);

    const hydration = dashboard.hydrate();
    const refresh = dashboard.refreshAttention();

    refreshedAttention.resolve({ items: [makeItem({ id: "live" })] });
    await refresh;

    hydrationAttention.resolve({ items: [makeItem({ id: "stale" })] });
    hydrationConnections.resolve({
      connections: [makeConnection()],
      update: { available: false, in_container: false },
    });
    hydrationSyncLog.resolve({ entries: [] });
    await hydration;

    expect(dashboard.items.map((item) => item.id)).toEqual(["live"]);
  });
});
