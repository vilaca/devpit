// The dashboard's reactive state and its live-sync wiring, in one place.
//
// Two requirements shape this module:
//   1. Update without a refresh — an open SSE stream (sse.ts) invalidates the
//      relevant slice, which re-fetches from REST and reactively re-renders.
//   2. Correct on refresh — a cold page load runs the same hydrate() path, and
//      because the backend folds state on read, a reload and a live update
//      converge on identical data. There is no client-persisted state to drift.
//
// State lives in Svelte 5 runes ($state), so any component that reads
// `dashboard.*` re-renders when these change.

import { getAttention, getConnections, getSyncLog, setFlag, clearFlag } from "./api";
import { connectEvents, type ConnectionState } from "./sse";
import type { AttentionItem, Connection, SyncLogEntry, UpdateInfo } from "./types";

interface Banner {
  connectionId: string;
  label: string;
  cause: string;
}

class Dashboard {
  items = $state<AttentionItem[]>([]);
  connections = $state<Connection[]>([]);
  syncLog = $state<SyncLogEntry[]>([]);
  // Self-update hint (ADR-0023). Rides on /connections; the TopBar renders a
  // chip only when available. Null until the first hydrate.
  update = $state<UpdateInfo | null>(null);

  loading = $state(true);
  loadError = $state<string | null>(null);
  streamState = $state<ConnectionState>("connecting");
  // Non-blocking failure banner, driven by sync.failed (ADR-0018). Dismissable
  // client-side; the next sync.failed re-raises it.
  banner = $state<Banner | null>(null);

  #disposeSse: (() => void) | null = null;
  // Coalesce bursts of attention.changed into a single re-fetch.
  #attentionTimer: ReturnType<typeof setTimeout> | null = null;

  // start hydrates everything, then opens the live stream. Returns a disposer
  // for onDestroy. Idempotent guards are unnecessary — App calls it once.
  start(): () => void {
    void this.hydrate();
    this.#disposeSse = connectEvents({
      onOpen: () => {
        // Re-hydrate on (re)connect so anything that changed while the socket
        // was down is reconciled.
        void this.hydrate();
      },
      onAttentionChanged: () => this.#scheduleAttentionRefetch(),
      onSyncCompleted: () => {
        void this.refreshConnections();
        void this.refreshSyncLog();
      },
      onSyncFailed: (p) => {
        this.banner = {
          connectionId: p.connection_id,
          label: this.labelFor(p.connection_id),
          cause: p.cause ?? "sync failed",
        };
        void this.refreshConnections();
        void this.refreshSyncLog();
      },
      // The update hint travels on /connections, so re-fetch that slice.
      onUpdateAvailable: () => void this.refreshConnections(),
      onStateChange: (s) => {
        this.streamState = s;
      },
    });
    return () => {
      this.#disposeSse?.();
      if (this.#attentionTimer) clearTimeout(this.#attentionTimer);
    };
  }

  async hydrate(): Promise<void> {
    try {
      const [attention, connections, syncLog] = await Promise.all([
        getAttention(),
        getConnections(),
        getSyncLog(),
      ]);
      this.items = attention.items;
      this.connections = connections.connections;
      this.update = connections.update;
      this.syncLog = syncLog.entries;
      this.loadError = null;
    } catch (err) {
      this.loadError = err instanceof Error ? err.message : "failed to load";
    } finally {
      this.loading = false;
    }
  }

  async refreshAttention(): Promise<void> {
    try {
      this.items = (await getAttention()).items;
    } catch {
      // transient; the next event or reconnect re-hydrates
    }
  }

  async refreshConnections(): Promise<void> {
    try {
      const resp = await getConnections();
      this.connections = resp.connections;
      this.update = resp.update;
    } catch {
      /* transient */
    }
  }

  async refreshSyncLog(): Promise<void> {
    try {
      this.syncLog = (await getSyncLog()).entries;
    } catch {
      /* transient */
    }
  }

  #scheduleAttentionRefetch(): void {
    if (this.#attentionTimer) clearTimeout(this.#attentionTimer);
    this.#attentionTimer = setTimeout(() => void this.refreshAttention(), 150);
  }

  labelFor(connectionId: string): string {
    return this.connections.find((c) => c.id === connectionId)?.label ?? connectionId;
  }

  dismissBanner(): void {
    this.banner = null;
  }

  // toggleFlag applies the pin optimistically, then persists. On failure it
  // rolls back; a real change also arrives as attention.changed and re-fetches.
  async toggleFlag(item: AttentionItem): Promise<void> {
    const next = !item.flagged;
    item.flagged = next;
    try {
      await (next ? setFlag(item.id) : clearFlag(item.id));
    } catch {
      item.flagged = !next; // rollback
    }
  }
}

export const dashboard = new Dashboard();
