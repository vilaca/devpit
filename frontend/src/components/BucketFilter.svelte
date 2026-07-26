<script lang="ts">
  import type { AttentionItem, Connection, Filter } from "../lib/types";
  import { visibleBuckets } from "../lib/buckets";

  const {
    items,
    connections,
    active,
    onSelect,
  }: {
    items: AttentionItem[];
    connections: Connection[];
    active: Filter | null;
    onSelect: (s: Filter | null) => void;
  } = $props();

  // Only show buckets that have items, so the filter bar stays uncluttered.
  const visible = $derived(visibleBuckets(items, connections));
  const total = $derived(items.filter((i) => !i.flagged).length);
</script>

{#if visible.length > 1}
  <div class="bar" role="navigation" aria-label="Bucket filter">
    <button
      class="chip"
      class:active={active === null}
      onclick={() => onSelect(null)}
    >
      All <span class="badge">{total}</span>
    </button>
    {#each visible as b (b.key)}
      <button
        class="chip"
        class:active={active === b.key}
        onclick={() => onSelect(active === b.key ? null : b.key)}
      >
        {b.label}
        <span class="badge">{b.count}</span>
      </button>
    {/each}
  </div>
{/if}

<style>
  .bar {
    display: flex;
    flex-wrap: wrap;
    gap: 6px;
    margin-bottom: 16px;
  }
  .chip {
    display: inline-flex;
    align-items: center;
    gap: 5px;
    padding: 4px 10px;
    border-radius: 14px;
    border: 1px solid var(--border);
    background: var(--bg-raised);
    color: var(--text-muted);
    font-size: 12px;
    white-space: nowrap;
    transition: border-color 0.1s, color 0.1s;
  }
  .chip:hover {
    border-color: var(--border-strong);
    color: var(--text);
  }
  .chip.active {
    border-color: var(--accent);
    color: var(--accent);
    background: color-mix(in srgb, var(--accent) 8%, var(--bg-raised));
  }
  .badge {
    font-size: 11px;
    opacity: 0.75;
  }
</style>
