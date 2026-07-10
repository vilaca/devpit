<script lang="ts">
  import { onMount } from "svelte";
  import { dashboard } from "./lib/dashboard.svelte";
  import type { State, AttentionItem } from "./lib/types";
  import { BUCKETS, countByState } from "./lib/buckets";
  import TopBar from "./components/TopBar.svelte";
  import FailureBanner from "./components/FailureBanner.svelte";
  import BucketFilter from "./components/BucketFilter.svelte";
  import AttentionList from "./components/AttentionList.svelte";
  import SyncLogDrawer from "./components/SyncLogDrawer.svelte";

  // --- URL state -------------------------------------------------------
  // Bucket filter + sync-log open state live in the URL so a browser refresh
  // restores the view. No router needed — one query string, history.replaceState.

  function readUrl(): { bucket: State | null; log: boolean; logConn: string | null } {
    const p = new URLSearchParams(location.search);
    return {
      bucket: (p.get("bucket") as State | null) ?? null,
      log: p.has("log"),
      logConn: p.get("logconn") ?? null,
    };
  }

  function writeUrl(state: { bucket: State | null; log: boolean; logConn: string | null }) {
    const p = new URLSearchParams();
    if (state.bucket) p.set("bucket", state.bucket);
    if (state.log) p.set("log", "1");
    if (state.log && state.logConn) p.set("logconn", state.logConn);
    const qs = p.toString();
    history.replaceState({}, "", qs ? "?" + qs : location.pathname);
  }

  const initial = readUrl();
  let activeBucket = $state<State | null>(initial.bucket);
  let logOpen = $state(initial.log);
  let logConnFilter = $state<string | null>(initial.logConn);

  // Sync URL whenever state changes.
  $effect(() => {
    writeUrl({ bucket: activeBucket, log: logOpen, logConn: logConnFilter });
  });

  function setBucket(s: State | null) {
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

  // The ordered visible list: pinned (in flag order) then ranked (filtered).
  // Keyboard nav steps through this list.
  const visibleItems = $derived.by<AttentionItem[]>(() => {
    const pinned = dashboard.items.filter((i) => i.flagged);
    const ranked = dashboard.items.filter((i: AttentionItem) => {
      if (i.flagged) return false;
      if (!activeBucket) return true;
      return i.states.includes(activeBucket!);
    });
    return [...pinned, ...ranked];
  });

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
          const counts = countByState(dashboard.items);
          const visible = BUCKETS.filter((b) => (counts.get(b.state) ?? 0) > 0);
          if (visible.length === 0) break;
          const idx = visible.findIndex((b) => b.state === activeBucket);
          activeBucket = idx === -1 ? visible[0].state : (visible[(idx + 1) % visible.length]?.state ?? null);
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
  <title>DevPit{activeBucket ? ` · ${activeBucket.replace(/_/g, " ")}` : ""}</title>
</svelte:head>

<div class="layout">
  <TopBar
    connections={dashboard.connections}
    streamState={dashboard.streamState}
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
        active={activeBucket}
        onSelect={setBucket}
      />
      <AttentionList
        items={dashboard.items}
        activeState={activeBucket}
        {focusedId}
        onToggleFlag={(item) => void dashboard.toggleFlag(item)}
        onFocus={(id) => { focusedId = id; }}
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
    </footer>
  {/if}
</div>

{#if logOpen}
  <SyncLogDrawer
    entries={dashboard.syncLog}
    connections={dashboard.connections}
    filterConnectionId={logConnFilter}
    onClose={closeLog}
    onFilterChange={(id) => { logConnFilter = id; }}
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
</style>
