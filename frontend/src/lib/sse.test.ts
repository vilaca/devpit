import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import {
  errorState,
  reconnectDelay,
  connectEvents,
  type SseHandlers,
  type ConnectionState,
} from "./sse";

// Minimal EventSource stand-in: records instances and lets tests drive open /
// error events and readyState directly.
class MockEventSource {
  static readonly CONNECTING = 0;
  static readonly OPEN = 1;
  static readonly CLOSED = 2;
  static instances: MockEventSource[] = [];

  readyState: number = MockEventSource.CONNECTING;
  private readonly listeners = new Map<string, Array<(e: Event) => void>>();

  constructor(readonly url: string) {
    MockEventSource.instances.push(this);
  }

  addEventListener(type: string, fn: (e: Event) => void): void {
    const arr = this.listeners.get(type) ?? [];
    arr.push(fn);
    this.listeners.set(type, arr);
  }

  close(): void {
    this.readyState = MockEventSource.CLOSED;
  }

  emit(type: string): void {
    for (const fn of this.listeners.get(type) ?? []) fn(new Event(type));
  }
}

function makeHandlers(states: ConnectionState[]): SseHandlers {
  return {
    onAttentionChanged: vi.fn(),
    onSyncCompleted: vi.fn(),
    onSyncFailed: vi.fn(),
    onUpdateAvailable: vi.fn(),
    onOpen: vi.fn(),
    onStateChange: (s) => {
      states.push(s);
    },
  };
}

describe("sse", () => {
  beforeEach(() => {
    MockEventSource.instances = [];
    vi.stubGlobal("EventSource", MockEventSource);
    vi.useFakeTimers();
  });
  afterEach(() => {
    vi.useRealTimers();
    vi.unstubAllGlobals();
  });

  it("errorState maps CLOSED to closed, anything else to connecting", () => {
    expect(errorState(MockEventSource.CLOSED)).toBe("closed");
    expect(errorState(MockEventSource.CONNECTING)).toBe("connecting");
    expect(errorState(MockEventSource.OPEN)).toBe("connecting");
  });

  it("reconnectDelay backs off exponentially, capped at 30s", () => {
    expect(reconnectDelay(0)).toBe(1000);
    expect(reconnectDelay(1)).toBe(2000);
    expect(reconnectDelay(2)).toBe(4000);
    expect(reconnectDelay(10)).toBe(30_000);
  });

  it("reconnects after a fatal CLOSE and re-enters connecting", () => {
    const states: ConnectionState[] = [];
    const handlers = makeHandlers(states);
    const dispose = connectEvents(handlers);

    expect(MockEventSource.instances).toHaveLength(1);
    const first = MockEventSource.instances[0];

    first.readyState = MockEventSource.OPEN;
    first.emit("open");
    expect(vi.mocked(handlers.onOpen)).toHaveBeenCalledOnce();
    expect(states).toContain("open");

    // Non-retriable error: EventSource sits at CLOSED and never reconnects.
    first.readyState = MockEventSource.CLOSED;
    first.emit("error");
    expect(states).toContain("closed");

    // Our backoff recreates the stream.
    vi.advanceTimersByTime(reconnectDelay(0));
    expect(MockEventSource.instances).toHaveLength(2);

    dispose();
  });

  it("does not self-reconnect on a transient (CONNECTING) error", () => {
    const states: ConnectionState[] = [];
    const dispose = connectEvents(makeHandlers(states));

    const first = MockEventSource.instances[0];
    first.readyState = MockEventSource.CONNECTING;
    first.emit("error");

    vi.advanceTimersByTime(60_000);
    expect(MockEventSource.instances).toHaveLength(1);

    dispose();
  });

  it("stops reconnecting once disposed", () => {
    const states: ConnectionState[] = [];
    const dispose = connectEvents(makeHandlers(states));

    const first = MockEventSource.instances[0];
    first.readyState = MockEventSource.CLOSED;
    first.emit("error");

    dispose();
    vi.advanceTimersByTime(60_000);
    expect(MockEventSource.instances).toHaveLength(1);
  });
});
