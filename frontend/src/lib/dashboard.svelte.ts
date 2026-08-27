// The dashboard's reactive state and its live-sync wiring, in one place.
//
// Two requirements shape this module:
//   1. Update without a refresh — an open SSE stream (sse.ts) invalidates the
//      relevant slice, which re-fetches from REST and reactively re-renders.
//   2. Correct on refresh — a cold page load runs the same hydrate() path, and
//      because the backend folds state on read, a reload and a live update
//      converge on identical data. There is no client-persisted state to drift.
//
// After the first successful hydrate, a REST failure keeps the last snapshot
// (stale=true → failing-color list edge) and retries with backoff. SSE
// reconnect is independent and does not interpret HTTP 429.
//
// State lives in Svelte 5 runes ($state), so any component that reads
// `dashboard.*` re-renders when these change.

import {
  getAttention,
  getConnections,
  getSyncLog,
  setFlag,
  clearFlag,
  ApiRequestError,
} from "./api";
import { connectEvents, type ConnectionState } from "./sse";
import { logInfo, logWarn } from "./log";
import { isRetriable, retryDelay, retryHintMs } from "./retry";
import type {
  AttentionItem,
  Connection,
  SyncLogEntry,
  UpdateInfo,
} from "./types";

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
  // True after a successful load when a later REST hydrate/refresh failed.
  // The list stays; App paints a failing-color edge so the snapshot is visibly
  // stale (ADR-0018: keep last good data).
  stale = $state(false);
  streamState = $state<ConnectionState>("connecting");
  // Non-blocking failure banner, driven by sync.failed (ADR-0018). Dismissable
  // client-side; the next sync.failed re-raises it.
  banner = $state<Banner | null>(null);

  #disposeSse: (() => void) | null = null;
  // Coalesce bursts of attention.changed into a single re-fetch.
  #attentionTimer: ReturnType<typeof setTimeout> | null = null;
  #retryTimer: ReturnType<typeof setTimeout> | null = null;
  #retryAttempt = 0;
  // Wall-clock time (ms) the pending retry will fire, so a later failure with a
  // longer Retry-After hint can push the retry back instead of being dropped.
  #retryDeadline = 0;
  // First successful hydrate has applied; later failures must not blank the list.
  #ready = false;
  // A reconnect can start a newer hydrate before an earlier request completes.
  // Only the latest snapshot may replace the dashboard state.
  #hydrateGeneration = 0;
  // A live slice refresh may complete before an older hydration. Each writer
  // advances its slice generation so stale responses cannot overwrite it.
  #attentionGeneration = 0;
  #connectionsGeneration = 0;
  #syncLogGeneration = 0;

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
      if (this.#retryTimer) clearTimeout(this.#retryTimer);
    };
  }

  async hydrate(): Promise<void> {
    this.#cancelRetry();
    const generation = ++this.#hydrateGeneration;
    const attentionGeneration = ++this.#attentionGeneration;
    const connectionsGeneration = ++this.#connectionsGeneration;
    const syncLogGeneration = ++this.#syncLogGeneration;
    try {
      const [attention, connections, syncLog] = await Promise.all([
        getAttention(),
        getConnections(),
        getSyncLog(),
      ]);
      if (generation !== this.#hydrateGeneration) return;
      if (attentionGeneration === this.#attentionGeneration) {
        this.items = attention.items;
        // Clear stale only while this hydrate's attention slice is still the
        // newest: a later refreshAttention (which may have failed and set
        // stale) owns the flag, and this older success must not clear its
        // error state (INV-9). Mirrors refreshAttention's guarded recovery.
        if (this.stale) {
          this.stale = false;
          logInfo("hydrate recovered");
        }
      }
      if (connectionsGeneration === this.#connectionsGeneration) {
        this.connections = connections.connections;
        this.update = connections.update;
      }
      if (syncLogGeneration === this.#syncLogGeneration) {
        this.syncLog = syncLog.entries;
      }
      this.#markFresh();
    } catch (err) {
      if (generation !== this.#hydrateGeneration) return;
      this.#onHydrateFailure(err);
    } finally {
      if (generation === this.#hydrateGeneration) this.loading = false;
    }
  }

  async refreshAttention(): Promise<void> {
    const generation = ++this.#attentionGeneration;
    try {
      const attention = await getAttention();
      if (generation === this.#attentionGeneration) {
        this.items = attention.items;
        if (this.stale) {
          this.stale = false;
          logInfo("list refresh recovered");
        }
      }
    } catch (err) {
      if (generation !== this.#attentionGeneration) return;
      if (this.#ready) {
        this.stale = true;
        logWarn("list refresh failed; keeping last snapshot", fieldsFor(err));
        this.#scheduleHydrateRetry(err);
      }
    }
  }

  async refreshConnections(): Promise<void> {
    const generation = ++this.#connectionsGeneration;
    try {
      const resp = await getConnections();
      if (generation === this.#connectionsGeneration) {
        this.connections = resp.connections;
        this.update = resp.update;
      }
    } catch {
      /* transient */
    }
  }

  async refreshSyncLog(): Promise<void> {
    const generation = ++this.#syncLogGeneration;
    try {
      const syncLog = await getSyncLog();
      if (generation === this.#syncLogGeneration) {
        this.syncLog = syncLog.entries;
      }
    } catch {
      /* transient */
    }
  }

  #scheduleAttentionRefetch(): void {
    if (this.#attentionTimer) clearTimeout(this.#attentionTimer);
    this.#attentionTimer = setTimeout(() => void this.refreshAttention(), 150);
  }

  labelFor(connectionId: string): string {
    return (
      this.connections.find((c) => c.id === connectionId)?.label ?? connectionId
    );
  }

  dismissBanner(): void {
    this.banner = null;
  }

  #markFresh(): void {
    // stale is an attention-slice flag, cleared in hydrate()'s guarded
    // attention block (and in refreshAttention) so a newer request's error
    // survives an older success. Here we settle only the cold-load flags,
    // which no newer request owns.
    const recovered = this.loadError !== null;
    this.#ready = true;
    this.#retryAttempt = 0;
    this.loadError = null;
    if (recovered) logInfo("hydrate recovered");
  }

  #onHydrateFailure(err: unknown): void {
    const fields = fieldsFor(err);
    if (this.#ready) {
      this.stale = true;
      logWarn("hydrate failed; keeping last snapshot", fields);
    } else {
      this.loadError = err instanceof Error ? err.message : "failed to load";
      logWarn("hydrate failed", fields);
    }
    this.#scheduleHydrateRetry(err);
  }

  #scheduleHydrateRetry(err: unknown): void {
    if (!isRetriable(err)) {
      // A non-retriable failure schedules nothing, but must not cancel a retry
      // already pending from an earlier retriable failure.
      if (!this.#retryTimer) {
        logWarn("hydrate not retried", { ...fieldsFor(err), retriable: false });
      }
      return;
    }
    const delay = retryDelay(this.#retryAttempt, retryHintMs(err));
    const deadline = Date.now() + delay;
    if (this.#retryTimer) {
      // A retry is already pending. Replace it only when this failure's hint
      // pushes the fire time later (e.g. a 429 Retry-After longer than a
      // pending backoff step) — a shorter delay must never pull the retry
      // earlier into a rate-limit window and trigger another 429.
      if (deadline <= this.#retryDeadline) return;
      clearTimeout(this.#retryTimer);
    } else {
      this.#retryAttempt++;
    }
    this.#retryDeadline = deadline;
    logWarn("hydrate retry scheduled", {
      ...fieldsFor(err),
      attempt: this.#retryAttempt,
      delay_ms: delay,
    });
    this.#retryTimer = setTimeout(() => {
      this.#retryTimer = null;
      void this.hydrate();
    }, delay);
  }

  #cancelRetry(): void {
    if (!this.#retryTimer) return;
    clearTimeout(this.#retryTimer);
    this.#retryTimer = null;
    this.#retryDeadline = 0;
    logInfo("hydrate retry cancelled");
  }

  // resetForTest returns the singleton to a cold-load state so tests do not
  // leak #ready, stale, or a pending retry timer across cases.
  resetForTest(): void {
    this.items = [];
    this.connections = [];
    this.syncLog = [];
    this.update = null;
    this.loading = true;
    this.loadError = null;
    this.stale = false;
    this.banner = null;
    this.#ready = false;
    this.#retryAttempt = 0;
    this.#cancelRetry();
    if (this.#attentionTimer) {
      clearTimeout(this.#attentionTimer);
      this.#attentionTimer = null;
    }
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

function fieldsFor(err: unknown): Record<string, unknown> {
  const fields: Record<string, unknown> = {};
  if (err instanceof ApiRequestError) {
    fields.status = err.status;
    fields.code = err.code;
    fields.err = err.message;
    if (err.retryAfterMs != null) fields.retry_after_ms = err.retryAfterMs;
  } else if (err instanceof Error) {
    fields.err = err.message;
    fields.kind = err.name;
  } else {
    fields.err = String(err);
  }
  return fields;
}

export const dashboard = new Dashboard();
