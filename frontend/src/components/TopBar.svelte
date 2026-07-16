<script lang="ts">
  import type { Connection, UpdateInfo } from "../lib/types";
  import type { ConnectionState } from "../lib/sse";
  import ConnectionHealth from "./ConnectionHealth.svelte";

  const {
    connections,
    streamState,
    update,
    onShowLog,
  }: {
    connections: Connection[];
    streamState: ConnectionState;
    update: UpdateInfo | null;
    onShowLog: (connectionId?: string) => void;
  } = $props();

  const streamTitle: Record<ConnectionState, string> = {
    open: "Live — updates in real time",
    connecting: "Reconnecting to live stream…",
    closed: "Live stream closed",
  };

  // Upgrade command shown on hover: docker inside a container, brew otherwise
  // (ADR-0023). The backend reports which via update.in_container.
  const upgradeCmd = $derived(
    update?.in_container
      ? "docker pull ghcr.io/vilaca/devpit"
      : "brew upgrade vilaca/devpit/devpit",
  );
  const updateHint = $derived(
    update?.latest_version
      ? `${update.latest_version} available — ${upgradeCmd}`
      : upgradeCmd,
  );
</script>

<header class="topbar">
  <a class="brand" href="/" onclick={(e) => e.preventDefault()}>DevPit</a>

  {#if streamState !== "open"}
    <button
      class="stream stream-{streamState}"
      title={streamTitle[streamState]}
      onclick={() => onShowLog()}
      aria-label="Open sync log"
    >
      <span class="dot"></span>
      <span class="sr-only">{streamState}</span>
    </button>
  {/if}

  <nav class="connections">
    {#if update?.available}
      <a
        class="update"
        href={update.release_url}
        target="_blank"
        rel="noopener noreferrer"
        title={updateHint}
      >
        Update
      </a>
    {/if}
    {#each connections as c (c.id)}
      <ConnectionHealth connection={c} onShowLog={(id: string) => onShowLog(id)} />
    {/each}
  </nav>
</header>

<style>
  .topbar {
    display: flex;
    align-items: center;
    gap: 12px;
    padding: 0 16px;
    height: 44px;
    background: var(--bg-raised);
    border-bottom: 1px solid var(--border);
    flex-shrink: 0;
  }
  .brand {
    font-weight: 700;
    font-size: 15px;
    color: var(--text);
    letter-spacing: -0.01em;
    margin-right: 4px;
  }
  .connections {
    display: flex;
    align-items: center;
    gap: 2px;
    margin-left: auto;
  }
  .update {
    font-size: 11px;
    font-weight: 600;
    line-height: 1;
    padding: 3px 8px;
    margin-right: 6px;
    border: 1px solid var(--accent);
    border-radius: 999px;
    color: var(--accent);
    text-decoration: none;
    white-space: nowrap;
  }
  .update:hover {
    background: var(--accent);
    color: var(--bg);
  }
  .stream {
    border: none;
    background: none;
    padding: 4px;
    display: flex;
    align-items: center;
  }
  .dot {
    width: 8px;
    height: 8px;
    border-radius: 50%;
    background: var(--text-faint);
  }
  .stream-open .dot {
    background: var(--health-ok);
  }
  .stream-connecting .dot {
    background: var(--health-degraded);
    animation: pulse 1.2s ease-in-out infinite;
  }
  @keyframes pulse {
    0%, 100% { opacity: 1; }
    50% { opacity: 0.4; }
  }
  .sr-only {
    position: absolute;
    width: 1px;
    height: 1px;
    overflow: hidden;
    clip: rect(0 0 0 0);
  }
</style>
