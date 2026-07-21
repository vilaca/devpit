<script lang="ts">
  import type { AttentionItem, State } from "../lib/types";
  import { BUCKETS, countByState } from "../lib/buckets";

  const {
    items,
    active,
    onSelect,
  }: {
    items: AttentionItem[];
    active: State | null;
    onSelect: (s: State | null) => void;
  } = $props();

  const counts = $derived(countByState(items));
  // Only show buckets that have items, so the filter bar stays uncluttered.
  const visible = $derived(BUCKETS.filter((b) => (counts.get(b.state) ?? 0) > 0));
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
    {#each visible as b (b.state)}
      <button
        class="chip"
        class:active={active === b.state}
        onclick={() => onSelect(active === b.state ? null : b.state)}
      >
        {b.label}
        <span class="badge">{counts.get(b.state)}</span>
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
