<script lang="ts">
  import type { AttentionItem, Connection, Filter } from "../lib/types";
  import { matchesFilter } from "../lib/buckets";
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

  // Pinned items float above the ranked list in flag order, never filtered
  // by bucket — they're the user's explicit priority regardless of state.
  const pinned = $derived(items.filter((i) => i.flagged));
  const ranked = $derived(
    items.filter((i) => !i.flagged && matchesFilter(i, activeFilter, connections)),
  );
</script>

<div class="list">
  <PinnedZone items={pinned} {focusedId} {onToggleFlag} {onFocus} />

  {#if ranked.length > 0}
    <section>
      <h2>
        {#if activeFilter}
          Filtered
        {:else}
          Attention
        {/if}
        <span class="count">({ranked.length})</span>
      </h2>
      <div role="list">
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
