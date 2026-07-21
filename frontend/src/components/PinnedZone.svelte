<script lang="ts">
  import type { AttentionItem } from "../lib/types";
  import WorkItemRow from "./WorkItemRow.svelte";

  const {
    items,
    focusedId,
    onToggleFlag,
    onFocus,
  }: {
    items: AttentionItem[];
    focusedId: string | null;
    onToggleFlag: (item: AttentionItem) => void;
    onFocus: (id: string) => void;
  } = $props();
</script>

{#if items.length > 0}
  <section class="pinned-zone">
    <h2>Handle next</h2>
    <div role="list">
      {#each items as item (item.id)}
        <WorkItemRow
          {item}
          focused={focusedId === item.id}
          {onToggleFlag}
          {onFocus}
        />
      {/each}
    </div>
  </section>
{/if}

<style>
  .pinned-zone {
    margin-bottom: 8px;
  }
  h2 {
    font-size: 11px;
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: 0.06em;
    color: var(--marker-stale);
    margin: 0 0 8px;
    padding: 0 2px;
  }
</style>
