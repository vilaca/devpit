<script lang="ts">
  import type { Connection } from "../lib/types";
  import { relativeTime } from "../lib/format";

  const {
    connection,
    onShowLog,
  }: {
    connection: Connection;
    onShowLog: (connectionId: string) => void;
  } = $props();

  const tooltip = $derived(() => {
    const h = connection.health;
    const last = h.last_synced_at ? relativeTime(h.last_synced_at) : "never";
    const who = connection.identity ?? connection.label;
    return `${who} · ${h.status} · synced ${last}`;
  });
</script>

<button
  class="conn"
  class:ok={connection.health.status === "ok"}
  class:degraded={connection.health.status === "degraded"}
  class:failing={connection.health.status === "failing"}
  title={tooltip()}
  onclick={() => onShowLog(connection.id)}
  aria-label="{connection.label} sync status: {connection.health.status}"
>
  <span class="dot"></span>
  <span class="label">{connection.label}</span>
</button>

<style>
  .conn {
    display: inline-flex;
    align-items: center;
    gap: 5px;
    border: none;
    background: none;
    color: var(--text-muted);
    font-size: 12px;
    padding: 2px 4px;
    border-radius: var(--radius-sm);
    cursor: pointer;
    transition: background 0.1s;
  }
  .conn:hover {
    background: var(--bg-subtle);
    color: var(--text);
  }
  .dot {
    width: 7px;
    height: 7px;
    border-radius: 50%;
    background: var(--text-faint);
    flex-shrink: 0;
  }
  .ok .dot {
    background: var(--health-ok);
  }
  .degraded .dot {
    background: var(--health-degraded);
  }
  .failing .dot {
    background: var(--health-failing);
  }
</style>
