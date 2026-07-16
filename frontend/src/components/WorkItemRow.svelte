<script lang="ts">
  import type { AttentionItem } from "../lib/types";
  import { relativeTime } from "../lib/format";
  import { dashboard } from "../lib/dashboard.svelte";
  import { isMine } from "../lib/buckets";
  import StateTags from "./StateTags.svelte";
  import Labels from "./Labels.svelte";

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

  const mine = $derived(isMine(item, dashboard.connections));

  // Approval count, phrased to surface that *you* approved when you did.
  const approvalsLabel = $derived.by(() => {
    const n = item.approvals_count;
    if (item.my_review_state === "approved") {
      const others = n - 1;
      return others > 0 ? `you + ${others} approved` : "you approved";
    }
    return `${n} approved`;
  });

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
  class:mine
  class:muted={item.muted}
  class:old={item.old}
  onclick={() => onFocus(item.id)}
  onkeydown={(e) => {
    if (e.key === "Enter" || e.key === " ") onFocus(item.id);
  }}
  role="option"
  aria-selected={focused}
  tabindex="0"
>
  <button
    class="pin"
    class:is-flagged={item.flagged}
    title={item.flagged ? "Remove from Handle next" : "Mark as Handle next"}
    onclick={(e) => {
      e.stopPropagation();
      onToggleFlag(item);
    }}
    aria-label={item.flagged ? "Unpin" : "Pin"}
  >
    {item.flagged ? "★" : "☆"}
  </button>

  <div class="body">
    <div class="title-line">
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
      {#if !item.muted}
        <div class="tags-wrap"><StateTags {item} /></div>
      {/if}
    </div>
    <div class="meta-row">
      {#if item.repo}
        <span class="repo">{item.repo}</span>
        <span class="sep">·</span>
      {/if}
      <span class="conn">{item.connection_label}</span>
      <span class="sep">·</span>
      <span class="time" title={item.updated_at}
        >{relativeTime(item.updated_at)}</span
      >
      {#if item.author}
        <span class="sep">·</span>
        <span class="author">{item.author}</span>
      {/if}
      {#if item.jira}
        <span class="sep">·</span>
        <a
          class="jira-status"
          href={item.jira.url}
          target="_blank"
          rel="noopener noreferrer"
          title={item.jira.key}
          onclick={(e) => e.stopPropagation()}>{item.jira.status}</a
        >
      {/if}
      {#if item.approvals_count > 0}
        <span class="sep">·</span>
        <span class="approvals">{approvalsLabel}</span>
      {/if}
    </div>
    {#if item.labels && item.labels.length > 0}
      <Labels labels={item.labels} />
    {/if}
  </div>
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
  .row.mine {
    background: color-mix(in srgb, var(--accent) 7%, var(--bg-raised));
  }
  .row.old {
    background: color-mix(in srgb, var(--marker-old) 7%, var(--bg-raised));
  }
  /* Reviewed-done: nothing left for me. De-emphasized; full opacity on hover. */
  .row.muted {
    opacity: 0.55;
  }
  .row.muted:hover {
    opacity: 1;
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
  .title-line {
    display: flex;
    align-items: center;
    gap: 8px;
    min-width: 0;
  }
  /* Tags sit on the title line (top-right) and keep their width; the title
     truncates first so the meta-row below can span the full width. */
  .tags-wrap {
    flex-shrink: 0;
    margin-left: auto;
  }
  .jira-status {
    flex-shrink: 0;
    font-size: 12px;
    font-weight: 500;
    color: var(--text-faint);
    white-space: nowrap;
    text-decoration: none;
  }
  .jira-status:hover {
    color: var(--accent);
    text-decoration: underline;
  }
  .title {
    flex: 1;
    min-width: 0;
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
  .approvals,
  .time,
  .conn {
    white-space: nowrap;
  }
  .approvals {
    color: var(--meta-approvals);
    font-weight: 500;
  }
</style>
