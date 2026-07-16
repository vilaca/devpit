// EventSource wrapper for GET /events. The stream is coarse (docs/REST_API.md):
// each frame only says *that* something changed, so handlers re-fetch via REST
// rather than patch state from the payload. The browser's EventSource
// auto-reconnects with Last-Event-ID on transient drops; we add a small status
// callback so the UI can show a "reconnecting" hint, and we re-hydrate on
// (re)connect so any change missed while disconnected is picked up — this is
// what keeps a long-lived tab correct without a manual refresh.
//
// One thing EventSource does *not* do for us: on a non-retriable error (an HTTP
// error response, or an explicit server close) it goes to CLOSED permanently
// and never reconnects on its own. Left alone the UI would keep showing "Live"
// against a dead stream until a manual refresh, so on CLOSED we drive our own
// reconnect on a capped backoff (the onOpen re-hydrate then reconciles).

import type { SseEventName, ConnEventPayload } from "./types";

export type ConnectionState = "connecting" | "open" | "closed";

export interface SseHandlers {
  onAttentionChanged: () => void;
  onSyncCompleted: (payload: ConnEventPayload) => void;
  onSyncFailed: (payload: ConnEventPayload) => void;
  // Fired when the self-update status changes; the hint rides on /connections.
  onUpdateAvailable: () => void;
  // Fired on (re)connect so callers can re-hydrate and catch missed changes.
  onOpen: () => void;
  onStateChange?: (state: ConnectionState) => void;
}

// errorState maps an EventSource's readyState after an `error` event to the
// state we surface. CONNECTING = the browser is auto-retrying a transient drop;
// CLOSED = fatal (no auto-reconnect) — we must reconnect ourselves.
export function errorState(readyState: number): ConnectionState {
  return readyState === EventSource.CLOSED ? "closed" : "connecting";
}

// reconnectDelay is a capped exponential backoff (1s, 2s, 4s … max 30s) for the
// self-driven reconnect after a fatal close. `attempt` is 0-based.
export function reconnectDelay(attempt: number): number {
  return Math.min(30_000, 1000 * 2 ** attempt);
}

export function connectEvents(handlers: SseHandlers): () => void {
  let es: EventSource | null = null;
  let reconnectTimer: ReturnType<typeof setTimeout> | null = null;
  let attempt = 0;
  let disposed = false;

  function open(): void {
    const source = new EventSource("/events");
    es = source;
    handlers.onStateChange?.("connecting");

    source.addEventListener("open", () => {
      attempt = 0; // reset the backoff once a connection is established
      handlers.onStateChange?.("open");
      handlers.onOpen();
    });

    const named: Record<SseEventName, (e: MessageEvent) => void> = {
      "attention.changed": () => handlers.onAttentionChanged(),
      "sync.completed": (e) => handlers.onSyncCompleted(parse(e)),
      "sync.failed": (e) => handlers.onSyncFailed(parse(e)),
      "update.available": () => handlers.onUpdateAvailable(),
    };
    for (const [name, fn] of Object.entries(named)) {
      source.addEventListener(name, fn as EventListener);
    }

    source.addEventListener("error", () => {
      const state = errorState(source.readyState);
      handlers.onStateChange?.(state);
      // A CLOSED stream is fatal and never recovers on its own; drive our own
      // reconnect. A CONNECTING one means the browser is already retrying.
      if (state === "closed") scheduleReconnect();
    });
  }

  function scheduleReconnect(): void {
    if (disposed || reconnectTimer) return;
    es?.close();
    reconnectTimer = setTimeout(() => {
      reconnectTimer = null;
      if (!disposed) open();
    }, reconnectDelay(attempt++));
  }

  open();

  return () => {
    disposed = true;
    if (reconnectTimer) clearTimeout(reconnectTimer);
    es?.close();
    handlers.onStateChange?.("closed");
  };
}

function parse(e: MessageEvent): ConnEventPayload {
  try {
    return JSON.parse(e.data as string) as ConnEventPayload;
  } catch {
    return { connection_id: "" };
  }
}
