// EventSource wrapper for GET /events. The stream is coarse (docs/REST_API.md):
// each frame only says *that* something changed, so handlers re-fetch via REST
// rather than patch state from the payload. The browser's EventSource already
// auto-reconnects with Last-Event-ID; we add a small status callback so the UI
// can show a "reconnecting" hint, and we re-hydrate on (re)connect so any change
// missed while disconnected is picked up — this is what keeps a long-lived tab
// correct without a manual refresh.

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

export function connectEvents(handlers: SseHandlers): () => void {
  const es = new EventSource("/events");

  handlers.onStateChange?.("connecting");

  es.addEventListener("open", () => {
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
    es.addEventListener(name, fn as EventListener);
  }

  // EventSource reconnects on its own; surface the gap so the UI can hint.
  es.addEventListener("error", () => {
    if (es.readyState === EventSource.CONNECTING) handlers.onStateChange?.("connecting");
  });

  return () => {
    es.close();
    handlers.onStateChange?.("closed");
  };
}

function parse(e: MessageEvent): ConnEventPayload {
  try {
    return JSON.parse(e.data) as ConnEventPayload;
  } catch {
    return { connection_id: "" };
  }
}
