import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";

// Mock the REST layer so toggleFlag's optimistic-apply / rollback is observable
// without a backend. vi.mock is hoisted above the imports below.
vi.mock("./api", () => ({
  getAttention: vi.fn(),
  getConnections: vi.fn(),
  getSyncLog: vi.fn(),
  setFlag: vi.fn(),
  clearFlag: vi.fn(),
  ApiRequestError: class ApiRequestError extends Error {
    constructor(
      readonly status: number,
      readonly code: string,
      message: string,
      readonly retryAfterMs: number | null = null,
    ) {
      super(message);
      this.name = "ApiRequestError";
    }
  },
}));

import { dashboard } from "./dashboard.svelte";
import {
  setFlag,
  clearFlag,
  getAttention,
  getConnections,
  getSyncLog,
  ApiRequestError,
} from "./api";
import { makeConnection, makeItem } from "./fixtures";
import type {
  AttentionResponse,
  ConnectionsResponse,
  SyncLogResponse,
} from "./types";
import { deferred } from "./deferred";

function mockHydrateOk(itemId: string): void {
  vi.mocked(getAttention).mockResolvedValueOnce({
    items: [makeItem({ id: itemId })],
  });
  vi.mocked(getConnections).mockResolvedValueOnce({
    connections: [makeConnection()],
    update: { available: false, in_container: false },
  });
  vi.mocked(getSyncLog).mockResolvedValueOnce({ entries: [] });
}

function mockHydrateFail(err: Error): void {
  vi.mocked(getAttention).mockRejectedValueOnce(err);
  vi.mocked(getConnections).mockResolvedValueOnce({
    connections: [makeConnection()],
    update: { available: false, in_container: false },
  });
  vi.mocked(getSyncLog).mockResolvedValueOnce({ entries: [] });
}

describe("dashboard.toggleFlag", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    dashboard.resetForTest();
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
    dashboard.resetForTest();
  });

  afterEach(() => {
    dashboard.resetForTest();
    vi.useRealTimers();
    // Restore any console spies a case installed, so a spy left without an
    // explicit mockRestore does not silence console for later tests
    // (clearAllMocks resets call counts but not spy implementations).
    vi.restoreAllMocks();
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

  it("keeps the last snapshot and marks stale when a later hydrate fails", async () => {
    const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
    mockHydrateOk("ok");
    await dashboard.hydrate();
    expect(dashboard.stale).toBe(false);

    mockHydrateFail(new ApiRequestError(503, "unknown", "unavailable"));
    await dashboard.hydrate();

    expect(dashboard.items.map((item) => item.id)).toEqual(["ok"]);
    expect(dashboard.loadError).toBeNull();
    expect(dashboard.stale).toBe(true);
    expect(warn).toHaveBeenCalled();
    expect(
      warn.mock.calls.some((c) =>
        String(c[0]).includes("keeping last snapshot"),
      ),
    ).toBe(true);
    warn.mockRestore();
  });

  it("retries a retriable hydrate failure and clears stale on success", async () => {
    vi.useFakeTimers();
    const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
    const info = vi.spyOn(console, "info").mockImplementation(() => {});

    mockHydrateOk("ok");
    await dashboard.hydrate();

    mockHydrateFail(new TypeError("Failed to fetch"));
    await dashboard.hydrate();
    expect(dashboard.stale).toBe(true);
    expect(getAttention).toHaveBeenCalledTimes(2);

    mockHydrateOk("recovered");
    await vi.advanceTimersByTimeAsync(1000);
    expect(dashboard.items.map((item) => item.id)).toEqual(["recovered"]);
    expect(dashboard.stale).toBe(false);
    expect(
      info.mock.calls.some((c) => String(c[0]).includes("recovered")),
    ).toBe(true);

    warn.mockRestore();
    info.mockRestore();
  });

  it("waits Retry-After on 429 before retrying", async () => {
    vi.useFakeTimers();
    vi.spyOn(console, "warn").mockImplementation(() => {});

    mockHydrateOk("ok");
    await dashboard.hydrate();

    mockHydrateFail(
      new ApiRequestError(429, "unknown", "too many requests", 5_000),
    );
    await dashboard.hydrate();
    expect(getAttention).toHaveBeenCalledTimes(2);

    mockHydrateOk("later");
    await vi.advanceTimersByTimeAsync(4_999);
    expect(getAttention).toHaveBeenCalledTimes(2);

    await vi.advanceTimersByTimeAsync(1);
    expect(dashboard.items.map((item) => item.id)).toEqual(["later"]);
    expect(dashboard.stale).toBe(false);
  });

  it("extends a pending retry when a later Retry-After hint is longer", async () => {
    vi.useFakeTimers();
    vi.spyOn(console, "warn").mockImplementation(() => {});

    mockHydrateOk("ok");
    await dashboard.hydrate();
    expect(getAttention).toHaveBeenCalledTimes(1);

    // A live refresh fails with a short backoff (500 → ~1s), scheduling a retry.
    vi.mocked(getAttention).mockRejectedValueOnce(
      new ApiRequestError(500, "unknown", "server error"),
    );
    await dashboard.refreshAttention();
    expect(getAttention).toHaveBeenCalledTimes(2);

    // Before it fires, a second refresh fails with 429 Retry-After 5s. The
    // longer hint must push the pending retry back, not be dropped by the
    // shorter timer (which would fire mid-window and trigger another 429).
    vi.mocked(getAttention).mockRejectedValueOnce(
      new ApiRequestError(429, "unknown", "slow down", 5_000),
    );
    await dashboard.refreshAttention();
    expect(getAttention).toHaveBeenCalledTimes(3);

    // At the short delay the retry must NOT fire (the dropped-hint bug fired here).
    mockHydrateOk("recovered");
    await vi.advanceTimersByTimeAsync(1_000);
    expect(getAttention).toHaveBeenCalledTimes(3);

    // It fires once the longer Retry-After window elapses.
    await vi.advanceTimersByTimeAsync(4_000);
    expect(dashboard.items.map((item) => item.id)).toEqual(["recovered"]);
  });

  it("does not retry a non-retriable hydrate failure", async () => {
    vi.useFakeTimers();
    vi.spyOn(console, "warn").mockImplementation(() => {});

    mockHydrateOk("ok");
    await dashboard.hydrate();

    mockHydrateFail(new ApiRequestError(400, "bad_request", "nope"));
    await dashboard.hydrate();
    expect(dashboard.stale).toBe(true);

    await vi.advanceTimersByTimeAsync(30_000);
    expect(getAttention).toHaveBeenCalledTimes(2);
  });

  it("keeps stale when a newer refreshAttention failed before an older hydrate resolves", async () => {
    vi.spyOn(console, "warn").mockImplementation(() => {});
    vi.spyOn(console, "info").mockImplementation(() => {});

    mockHydrateOk("ok");
    await dashboard.hydrate();
    expect(dashboard.stale).toBe(false);

    // Start hydrate H2 with its fetches still in flight.
    const h2Attention = deferred<AttentionResponse>();
    const h2Connections = deferred<ConnectionsResponse>();
    const h2SyncLog = deferred<SyncLogResponse>();
    vi.mocked(getAttention).mockReturnValueOnce(h2Attention.promise);
    vi.mocked(getConnections).mockReturnValueOnce(h2Connections.promise);
    vi.mocked(getSyncLog).mockReturnValueOnce(h2SyncLog.promise);
    const h2 = dashboard.hydrate();

    // A newer refreshAttention supersedes H2's attention slice and fails,
    // setting stale=true.
    vi.mocked(getAttention).mockRejectedValueOnce(
      new ApiRequestError(503, "unknown", "unavailable"),
    );
    await dashboard.refreshAttention();
    expect(dashboard.stale).toBe(true);

    // H2 now resolves. Its attention write is skipped (superseded), and its
    // success must NOT clear the newer request's stale error state (INV-9).
    h2Attention.resolve({ items: [makeItem({ id: "h2" })] });
    h2Connections.resolve({
      connections: [makeConnection()],
      update: { available: false, in_container: false },
    });
    h2SyncLog.resolve({ entries: [] });
    await h2;

    expect(dashboard.stale).toBe(true);
    expect(dashboard.items.map((item) => item.id)).toEqual(["ok"]);
  });

  it("does not apply an older failure after a newer hydrate succeeds", async () => {
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

    firstAttention.reject(new TypeError("Failed to fetch"));
    await first;

    expect(dashboard.stale).toBe(false);
    expect(dashboard.loadError).toBeNull();
    expect(dashboard.items.map((item) => item.id)).toEqual(["new"]);
  });
});
