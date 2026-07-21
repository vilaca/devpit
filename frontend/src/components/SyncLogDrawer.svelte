<script lang="ts">
  import type { SyncLogEntry, Connection } from "../lib/types";
  import { relativeTime } from "../lib/format";

  const {
    entries,
    connections,
    filterConnectionId,
    onClose,
    onFilterChange,
  }: {
    entries: SyncLogEntry[];
    connections: Connection[];
    filterConnectionId: string | null;
    onClose: () => void;
    onFilterChange: (id: string | null) => void;
  } = $props();

  const shown = $derived(
    filterConnectionId
      ? entries.filter((e) => e.connection_id === filterConnectionId)
      : entries,
  );

  function outcomeClass(outcome: string): string {
    if (outcome === "ok") return "ok";
    if (outcome.startsWith("rate_limit")) return "warn";
    if (outcome.startsWith("error") || outcome === "fail") return "fail";
    return "";
  }
</script>

<!-- svelte-ignore a11y_click_events_have_key_events -->
<!-- svelte-ignore a11y_no_static_element_interactions -->
<div class="overlay" onclick={onClose} role="presentation"></div>
<aside class="drawer" aria-label="Sync log">
  <div class="header">
    <h2>Sync Log</h2>
    <div class="controls">
      <select
        value={filterConnectionId ?? ""}
        onchange={(e) => onFilterChange((e.target as HTMLSelectElement).value || null)}
        aria-label="Filter by connection"
      >
        <option value="">All connections</option>
        {#each connections as c (c.id)}
          <option value={c.id}>{c.label}</option>
        {/each}
      </select>
      <button class="close" onclick={onClose} aria-label="Close sync log">✕</button>
    </div>
  </div>

  {#if shown.length === 0}
    <p class="empty">No sync log entries.</p>
  {:else}
    <div class="log">
      {#each shown as entry (entry.id)}
        <div class="entry">
          <div class="entry-header">
            <span class="time" title={entry.ts}>{relativeTime(entry.ts)}</span>
            <span class="conn-label">{entry.connection_label}</span>
            <span class="op">{entry.operation === "fast_poll" ? "poll" : "reconcile"}</span>
            <span class="outcome {outcomeClass(entry.outcome)}">{entry.outcome}</span>
          </div>
          <div class="entry-body">
            {#if entry.items_changed > 0}
              <span>{entry.items_changed} item{entry.items_changed !== 1 ? "s" : ""} changed</span>
            {/if}
            {#if entry.rate_remaining !== null}
              <span>rate: {entry.rate_remaining} remaining</span>
            {/if}
            {#if entry.retries > 0}
              <span class="warn-text">{entry.retries} retr{entry.retries !== 1 ? "ies" : "y"}</span>
            {/if}
            {#if entry.error}
              <span class="error-text">{entry.error}</span>
            {/if}
          </div>
        </div>
      {/each}
    </div>
  {/if}
</aside>

<style>
  .overlay {
    position: fixed;
    inset: 0;
    background: rgba(0, 0, 0, 0.2);
    z-index: 100;
  }
  .drawer {
    position: fixed;
    top: 0;
    right: 0;
    height: 100%;
    width: min(480px, 95vw);
    background: var(--bg-raised);
    border-left: 1px solid var(--border);
    z-index: 101;
    display: flex;
    flex-direction: column;
    overflow: hidden;
  }
  .header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 12px;
    padding: 12px 16px;
    border-bottom: 1px solid var(--border);
    flex-shrink: 0;
  }
  h2 {
    font-size: 14px;
    margin: 0;
  }
  .controls {
    display: flex;
    align-items: center;
    gap: 8px;
  }
  select {
    font: inherit;
    font-size: 12px;
    padding: 3px 6px;
    border: 1px solid var(--border);
    border-radius: var(--radius-sm);
    background: var(--bg-subtle);
    color: var(--text);
  }
  .close {
    border: none;
    background: none;
    font-size: 14px;
    color: var(--text-faint);
    padding: 0 4px;
  }
  .close:hover {
    color: var(--text);
  }
  .log {
    overflow-y: auto;
    flex: 1;
    padding: 8px 0;
  }
  .entry {
    padding: 8px 16px;
    border-bottom: 1px solid var(--border);
    font-size: 12px;
  }
  .entry:last-child {
    border-bottom: none;
  }
  .entry-header {
    display: flex;
    align-items: center;
    gap: 8px;
    flex-wrap: wrap;
    margin-bottom: 3px;
  }
  .time {
    color: var(--text-faint);
    font-family: var(--font-mono);
    font-size: 11px;
  }
  .conn-label {
    font-weight: 500;
  }
  .op {
    color: var(--text-muted);
    font-size: 11px;
    background: var(--bg-subtle);
    padding: 1px 5px;
    border-radius: 3px;
  }
  .outcome {
    font-family: var(--font-mono);
    font-size: 11px;
  }
  .outcome.ok {
    color: var(--health-ok);
  }
  .outcome.warn {
    color: var(--health-degraded);
  }
  .outcome.fail {
    color: var(--health-failing);
  }
  .entry-body {
    display: flex;
    flex-wrap: wrap;
    gap: 10px;
    color: var(--text-muted);
  }
  .warn-text {
    color: var(--health-degraded);
  }
  .error-text {
    color: var(--health-failing);
  }
  .empty {
    padding: 20px 16px;
    color: var(--text-muted);
    font-size: 13px;
    margin: 0;
  }
</style>
