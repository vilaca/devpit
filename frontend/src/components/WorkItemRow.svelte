<script lang="ts">
  import type { AttentionItem } from "../lib/types";
  import { relativeTime } from "../lib/format";
  import StateTags from "./StateTags.svelte";

  const {
    item,
    focused = false,
    onToggleFlag,
    onFocus,
  }: {
    item: AttentionItem;
    focused?: boolean;
    onToggleFlag: (item: AttentionItem) => void;
    onFocus: (id: string) => void;
  } = $props();

  let rowEl: HTMLDivElement | undefined = $state();

  // Scroll focused row into view when keyboard nav lands on it.
  $effect(() => {
    if (focused && rowEl) rowEl.scrollIntoView({ block: "nearest" });
  });
</script>

<div
  bind:this={rowEl}
  class="row"
  class:focused
  onclick={() => onFocus(item.id)}
  onkeydown={(e) => { if (e.key === "Enter" || e.key === " ") onFocus(item.id); }}
  role="option"
  aria-selected={focused}
  tabindex="0"
>
  <button
    class="pin"
    class:is-flagged={item.flagged}
    title={item.flagged ? "Remove from Handle next" : "Mark as Handle next"}
    onclick={(e) => { e.stopPropagation(); onToggleFlag(item); }}
    aria-label={item.flagged ? "Unpin" : "Pin"}
  >
    {item.flagged ? "★" : "☆"}
  </button>

  <div class="body">
    <a
      class="title"
      href={item.url}
      target="_blank"
      rel="noopener noreferrer"
      title={item.title}
      onclick={(e) => e.stopPropagation()}
    >
      {item.title}
    </a>
    <div class="meta-row">
      <span class="repo">{item.repo || item.connection_label}</span>
      {#if item.author}
        <span class="sep">·</span>
        <span class="author">{item.author}</span>
      {/if}
      <span class="sep">·</span>
      <span class="time" title={item.updated_at}>{relativeTime(item.updated_at)}</span>
      <span class="sep">·</span>
      <span class="conn">{item.connection_label}</span>
    </div>
  </div>

  <StateTags {item} />
</div>

<style>
  .row {
    display: flex;
    align-items: flex-start;
    gap: 10px;
    padding: 9px 10px;
    background: var(--bg-raised);
    border: 1px solid var(--border);
    border-radius: var(--radius);
    margin-bottom: 5px;
    cursor: default;
    transition: border-color 0.1s;
  }
  .row:hover {
    border-color: var(--border-strong);
  }
  .row.focused {
    border-color: var(--accent);
    outline: none;
  }
  .pin {
    border: none;
    background: none;
    color: var(--text-faint);
    font-size: 15px;
    line-height: 1;
    padding: 2px 0;
    flex-shrink: 0;
    transition: color 0.1s;
  }
  .pin:hover,
  .pin.is-flagged {
    color: var(--marker-stale);
  }
  .body {
    flex: 1;
    min-width: 0;
    display: flex;
    flex-direction: column;
    gap: 3px;
  }
  .title {
    font-weight: 500;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    color: var(--text);
  }
  .title:hover {
    color: var(--accent);
    text-decoration: underline;
  }
  .meta-row {
    display: flex;
    align-items: center;
    gap: 5px;
    font-size: 12px;
    color: var(--text-faint);
    overflow: hidden;
  }
  .repo {
    font-family: var(--font-mono);
    font-size: 11px;
  }
  .sep {
    opacity: 0.5;
  }
  .author,
  .time,
  .conn {
    white-space: nowrap;
  }
</style>
