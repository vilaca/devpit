<script lang="ts">
  import type { AttentionItem, Connection, Filter } from "../lib/types";
  import { partitionVisible } from "../lib/buckets";
  import PinnedZone from "./PinnedZone.svelte";
  import WorkItemRow from "./WorkItemRow.svelte";

  const {
    items,
    connections,
    activeFilter,
    focusedId,
    onToggleFlag,
    onFocus,
  }: {
    items: AttentionItem[];
    connections: Connection[];
    activeFilter: Filter | null;
    focusedId: string | null;
    onToggleFlag: (item: AttentionItem) => void;
    onFocus: (id: string) => void;
  } = $props();

  // Pinned items float above the ranked list in flag order, never filtered by
  // bucket — they're the user's explicit priority regardless of state. Split via
  // the shared helper so keyboard nav (App) sees exactly these rows.
  const split = $derived(partitionVisible(items, activeFilter, connections));
  const pinned = $derived(split.pinned);
  const ranked = $derived(split.ranked);
</script>

<div class="list">
  <PinnedZone items={pinned} {focusedId} {onToggleFlag} {onFocus} />

  {#if ranked.length > 0}
    <section>
      <h2 id="attention-heading">
        {#if activeFilter}
          Filtered
        {:else}
          Attention
        {/if}
        <span class="count">({ranked.length})</span>
      </h2>
      <div role="listbox" aria-labelledby="attention-heading">
        {#each ranked as item (item.id)}
          <WorkItemRow
            {item}
            focused={focusedId === item.id}
            {onToggleFlag}
            {onFocus}
          />
        {/each}
      </div>
    </section>
  {:else if items.length > 0 && pinned.length === 0}
    <p class="empty">No items match this filter.</p>
  {:else if items.length === 0}
    <p class="empty">Nothing needs your attention right now.</p>
  {/if}
</div>

<style>
  .list {
    flex: 1;
  }
  h2 {
    font-size: 11px;
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: 0.06em;
    color: var(--text-muted);
    margin: 0 0 8px;
    padding: 0 2px;
  }
  .count {
    font-weight: 400;
    opacity: 0.7;
  }
  .empty {
    color: var(--text-muted);
    padding: 24px 0;
    margin: 0;
    font-size: 13px;
  }
</style>
