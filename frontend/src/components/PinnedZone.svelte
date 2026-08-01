<script lang="ts">
  import type { AttentionItem } from "../lib/types";
  import { relativeTime } from "../lib/format";
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
    <h2 id="pinned-heading">Handle next</h2>
    <div role="listbox" aria-labelledby="pinned-heading">
      {#each items as item (item.id)}
        <!-- Presentational wrapper: exists only to position the pin-age badge,
             so the WorkItemRow option stays a direct child of the listbox. -->
        <div class="pinned-item" role="presentation">
          <WorkItemRow
            {item}
            focused={focusedId === item.id}
            {onToggleFlag}
            {onFocus}
          />
          {#if item.flagged_at}
            <div class="pin-age">pinned {relativeTime(item.flagged_at)}</div>
          {/if}
        </div>
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
  .pinned-item {
    position: relative;
  }
  .pin-age {
    position: absolute;
    bottom: 6px;
    right: 10px;
    font-size: 10px;
    color: var(--text-faint);
    pointer-events: none;
  }
</style>
