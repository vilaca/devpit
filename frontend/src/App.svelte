<script lang="ts">
  import { onMount } from "svelte";
  import { SvelteURLSearchParams } from "svelte/reactivity";
  import { dashboard } from "./lib/dashboard.svelte";
  import type { Filter, AttentionItem } from "./lib/types";
  import { visibleBuckets, visibleOrder, parseFilter } from "./lib/buckets";
  import TopBar from "./components/TopBar.svelte";
  import FailureBanner from "./components/FailureBanner.svelte";
  import BucketFilter from "./components/BucketFilter.svelte";
  import AttentionList from "./components/AttentionList.svelte";
  import SyncLogDrawer from "./components/SyncLogDrawer.svelte";
  import { theme } from "./lib/theme.svelte";

  // --- URL state -------------------------------------------------------
  // Bucket filter + sync-log open state live in the URL so a browser refresh
  // restores the view. No router needed — one query string, history.replaceState.

  function readUrl(): {
    bucket: Filter | null;
    log: boolean;
    logConn: string | null;
  } {
    const p = new URLSearchParams(location.search);
    return {
      bucket: parseFilter(p.get("bucket")),
      log: p.has("log"),
      logConn: p.get("logconn") ?? null,
    };
  }

  function writeUrl(state: {
    bucket: Filter | null;
    log: boolean;
    logConn: string | null;
  }) {
    const p = new SvelteURLSearchParams();
    if (state.bucket) p.set("bucket", state.bucket);
    if (state.log) p.set("log", "1");
    if (state.log && state.logConn) p.set("logconn", state.logConn);
    const qs = p.toString();
    history.replaceState({}, "", qs ? "?" + qs : location.pathname);
  }

  const initial = readUrl();
  let activeBucket = $state<Filter | null>(initial.bucket);
  let logOpen = $state(initial.log);
  let logConnFilter = $state<string | null>(initial.logConn);

  // Sync URL whenever state changes.
  $effect(() => {
    writeUrl({ bucket: activeBucket, log: logOpen, logConn: logConnFilter });
  });

  function setBucket(s: Filter | null) {
    activeBucket = s;
  }

  function openLog(connectionId?: string) {
    logOpen = true;
    logConnFilter = connectionId ?? null;
  }

  function closeLog() {
    logOpen = false;
    logConnFilter = null;
  }

  // --- Keyboard navigation --------------------------------------------
  // j/k move focus, f toggles flag, Enter/o opens the item, / focuses the
  // filter. Escape closes the drawer or clears the active bucket.

  let focusedId = $state<string | null>(null);

  // The ordered visible list keyboard nav steps through — the same rows the
  // renderer shows, in the same order (derived once, in lib/buckets).
  const visibleItems = $derived(
    visibleOrder(dashboard.items, activeBucket, dashboard.connections),
  );

  function moveFocus(delta: 1 | -1) {
    const list = visibleItems;
    if (list.length === 0) return;
    const idx = focusedId ? list.findIndex((i) => i.id === focusedId) : -1;
    const next = Math.max(0, Math.min(list.length - 1, idx + delta));
    focusedId = list[next].id;
  }

  function openFocused() {
    if (!focusedId) return;
    const item = dashboard.items.find((i) => i.id === focusedId);
    if (item?.url) window.open(item.url, "_blank", "noopener,noreferrer");
  }

  function handleKey(e: KeyboardEvent) {
    // Don't intercept when typing in an input/select/textarea.
    const tag = (e.target as HTMLElement).tagName;
    if (tag === "INPUT" || tag === "TEXTAREA" || tag === "SELECT") return;
    if (e.metaKey || e.ctrlKey || e.altKey) return;

    switch (e.key) {
      case "j":
      case "ArrowDown":
        e.preventDefault();
        moveFocus(1);
        break;
      case "k":
      case "ArrowUp":
        e.preventDefault();
        moveFocus(-1);
        break;
      case "f":
        if (focusedId) {
          const item = dashboard.items.find((i) => i.id === focusedId);
          if (item) void dashboard.toggleFlag(item);
        }
        break;
      case "Enter":
      case "o":
        openFocused();
        break;
      case "/":
        // Cycle through visible buckets: null → first → … → last → null.
        e.preventDefault();
        {
          const visible = visibleBuckets(
            dashboard.items,
            dashboard.connections,
          );
          if (visible.length === 0) break;
          const idx = visible.findIndex((b) => b.key === activeBucket);
          activeBucket =
            idx === -1
              ? visible[0].key
              : (visible[(idx + 1) % visible.length]?.key ?? null);
        }
        break;
      case "Escape":
        if (logOpen) {
          closeLog();
        } else if (activeBucket !== null) {
          activeBucket = null;
        } else {
          focusedId = null;
        }
        break;
    }
  }

  onMount(() => {
    const dispose = dashboard.start();
    window.addEventListener("keydown", handleKey);
    return () => {
      dispose();
      window.removeEventListener("keydown", handleKey);
    };
  });
</script>

<svelte:head>
  <title
    >DevPit{activeBucket ? ` · ${activeBucket.replace(/_/g, " ")}` : ""}</title
  >
</svelte:head>

<div class="layout">
  <TopBar
    connections={dashboard.connections}
    streamState={dashboard.streamState}
    update={dashboard.update}
    onShowLog={openLog}
  />

  {#if dashboard.banner}
    <FailureBanner
      label={dashboard.banner.label}
      cause={dashboard.banner.cause}
      onDismiss={() => dashboard.dismissBanner()}
      onViewLog={() => openLog(dashboard.banner?.connectionId)}
    />
  {/if}

  <main class="main">
    {#if dashboard.loading}
      <p class="hint">Loading…</p>
    {:else if dashboard.loadError}
      <p class="hint error">Failed to load: {dashboard.loadError}</p>
    {:else}
      <BucketFilter
        items={dashboard.items}
        connections={dashboard.connections}
        active={activeBucket}
        onSelect={setBucket}
      />
      <AttentionList
        items={dashboard.items}
        connections={dashboard.connections}
        activeFilter={activeBucket}
        {focusedId}
        onToggleFlag={(item: AttentionItem) => void dashboard.toggleFlag(item)}
        onFocus={(id: string) => {
          focusedId = id;
        }}
      />
    {/if}
  </main>

  <!-- Keyboard shortcut hint -->
  {#if !dashboard.loading && !logOpen}
    <footer class="shortcuts">
      <kbd>j</kbd><kbd>k</kbd> navigate &nbsp;
      <kbd>f</kbd> pin &nbsp;
      <kbd>↵</kbd> open &nbsp;
      <kbd>/</kbd> filter &nbsp;
      <kbd>esc</kbd> clear
      <span class="shortcuts-sep"></span>
      <button
        class="theme-toggle"
        title={theme.dark ? "Switch to light mode" : "Switch to dark mode"}
        onclick={() => theme.toggle()}
        aria-label={theme.dark ? "Switch to light mode" : "Switch to dark mode"}
      >
        {#if theme.dark}
          <svg
            width="14"
            height="14"
            viewBox="0 0 16 16"
            fill="none"
            aria-hidden="true"
          >
            <circle
              cx="8"
              cy="8"
              r="3.5"
              stroke="currentColor"
              stroke-width="1.5"
            />
            <line
              x1="8"
              y1="1"
              x2="8"
              y2="2.5"
              stroke="currentColor"
              stroke-width="1.5"
              stroke-linecap="round"
            />
            <line
              x1="8"
              y1="13.5"
              x2="8"
              y2="15"
              stroke="currentColor"
              stroke-width="1.5"
              stroke-linecap="round"
            />
            <line
              x1="1"
              y1="8"
              x2="2.5"
              y2="8"
              stroke="currentColor"
              stroke-width="1.5"
              stroke-linecap="round"
            />
            <line
              x1="13.5"
              y1="8"
              x2="15"
              y2="8"
              stroke="currentColor"
              stroke-width="1.5"
              stroke-linecap="round"
            />
            <line
              x1="3.05"
              y1="3.05"
              x2="4.11"
              y2="4.11"
              stroke="currentColor"
              stroke-width="1.5"
              stroke-linecap="round"
            />
            <line
              x1="11.89"
              y1="11.89"
              x2="12.95"
              y2="12.95"
              stroke="currentColor"
              stroke-width="1.5"
              stroke-linecap="round"
            />
            <line
              x1="12.95"
              y1="3.05"
              x2="11.89"
              y2="4.11"
              stroke="currentColor"
              stroke-width="1.5"
              stroke-linecap="round"
            />
            <line
              x1="4.11"
              y1="11.89"
              x2="3.05"
              y2="12.95"
              stroke="currentColor"
              stroke-width="1.5"
              stroke-linecap="round"
            />
          </svg>
        {:else}
          <svg
            width="14"
            height="14"
            viewBox="0 0 16 16"
            fill="none"
            aria-hidden="true"
          >
            <path
              d="M13.5 9.5A6 6 0 1 1 6.5 2.5a4.5 4.5 0 0 0 7 7z"
              stroke="currentColor"
              stroke-width="1.5"
              stroke-linejoin="round"
            />
          </svg>
        {/if}
      </button>
    </footer>
  {/if}
</div>

{#if logOpen}
  <SyncLogDrawer
    entries={dashboard.syncLog}
    connections={dashboard.connections}
    filterConnectionId={logConnFilter}
    onClose={closeLog}
    onFilterChange={(id: string | null) => {
      logConnFilter = id;
    }}
  />
{/if}

<style>
  .layout {
    display: flex;
    flex-direction: column;
    height: 100dvh;
    overflow: hidden;
  }
  .main {
    flex: 1;
    overflow-y: auto;
    padding: 16px;
    max-width: 900px;
    width: 100%;
    margin: 0 auto;
    box-sizing: border-box;
  }
  .hint {
    color: var(--text-muted);
    padding: 24px 0;
    margin: 0;
  }
  .hint.error {
    color: var(--health-failing);
  }
  .shortcuts {
    display: flex;
    align-items: center;
    justify-content: center;
    gap: 2px;
    padding: 6px 16px;
    font-size: 11px;
    color: var(--text-faint);
    border-top: 1px solid var(--border);
    flex-shrink: 0;
  }
  kbd {
    display: inline-block;
    padding: 1px 4px;
    border: 1px solid var(--border-strong);
    border-radius: 3px;
    font-family: var(--font-mono);
    font-size: 10px;
    background: var(--bg-subtle);
    margin: 0 2px;
  }
  .shortcuts-sep {
    flex: 1;
  }
  .theme-toggle {
    border: none;
    background: none;
    padding: 2px 4px;
    display: flex;
    align-items: center;
    color: var(--text-faint);
    cursor: pointer;
  }
  .theme-toggle:hover {
    color: var(--text-muted);
  }
</style>
