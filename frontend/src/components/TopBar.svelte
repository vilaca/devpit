<script lang="ts">
  import type { Connection } from "../lib/types";
  import type { ConnectionState } from "../lib/sse";
  import ConnectionHealth from "./ConnectionHealth.svelte";

  const {
    connections,
    streamState,
    onShowLog,
  }: {
    connections: Connection[];
    streamState: ConnectionState;
    onShowLog: (connectionId?: string) => void;
  } = $props();

  const streamTitle: Record<ConnectionState, string> = {
    open: "Live — updates in real time",
    connecting: "Reconnecting to live stream…",
    closed: "Live stream closed",
  };
</script>

<header class="topbar">
  <a class="brand" href="/" onclick={(e) => e.preventDefault()}>DevPit</a>

  <nav class="connections">
    {#each connections as c (c.id)}
      <ConnectionHealth connection={c} onShowLog={(id) => onShowLog(id)} />
    {/each}
  </nav>

  <button
    class="stream stream-{streamState}"
    title={streamTitle[streamState]}
    onclick={() => onShowLog()}
    aria-label="Open sync log"
  >
    <span class="dot"></span>
    <span class="sr-only">{streamState}</span>
  </button>
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
