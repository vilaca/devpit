<script lang="ts">
  import type { AttentionItem, State } from "../lib/types";
  import { stateLabel, stateCSSVar, relativeTime } from "../lib/format";

  const { item }: { item: AttentionItem } = $props();

  // "Mentioned ×3" — signal_counts key is "mentioned" (fold.go strips prefix).
  function labelFor(s: State): string {
    if (s === "mentioned") {
      const n = item.signal_counts?.["mentioned"];
      if (n && n > 1) return `Mentioned ×${n}`;
    }
    return stateLabel(s);
  }

  // Hover text for a state chip. Rule: "{onset}" plus tag-specific extras.
  // Never restates the tag label. Omit tooltip when nothing to add.
  function titleForState(s: State): string | undefined {
    const onset = item.since?.[s];
    const dur = onset ? relativeTime(onset) : undefined;

    if (s === "blocked") {
      const detail = item.gate_detail ? ` · provider says: ${item.gate_detail}` : "";
      return dur ? `${dur}${detail}` : detail || undefined;
    }
    if (s === "ready_to_merge" && item.failing_checks) {
      return dur ? `${dur} · a non-required check is red` : "a non-required check is red";
    }
    if (s === "mentioned") {
      return dur ? `${dur} · clears when the item closes` : "clears when the item closes";
    }
    return dur;
  }

  // Hover text for a diagnostic marker badge.
  function titleForMarker(key: string): string | undefined {
    const onset = item.since?.[key];
    return onset ? relativeTime(onset) : undefined;
  }

  // When ready_to_merge + failing_checks co-occur, render them as one combined phrase.
  const readyButRed = $derived(
    item.states.includes("ready_to_merge") && item.failing_checks,
  );

  const staleTitle = $derived(
    `No activity for ${relativeTime(item.updated_at)} (threshold: 7 days)`,
  );
  const oldTitle = $derived(
    `No activity for ${relativeTime(item.updated_at)} (threshold: 30 days)`,
  );
</script>

<span class="tags">
  {#if item.draft}
    <span class="tag marker-draft" title={titleForMarker("draft")}>Draft</span>
  {/if}

  {#each item.states as s (s)}
    {#if s === "ready_to_merge" && readyButRed}
      <!-- Combined "ready · optional checks red" phrase — shown once -->
      <span
        class="tag"
        style:color={stateCSSVar(s)}
        style:border-color={stateCSSVar(s)}
        title={titleForState(s)}
      >Ready to Merge · optional checks red</span>
    {:else if s !== "ready_to_merge" || !readyButRed}
      <span
        class="tag"
        style:color={stateCSSVar(s)}
        style:border-color={stateCSSVar(s)}
        title={titleForState(s)}
      >{labelFor(s)}</span>
    {/if}
  {/each}

  {#if item.merge_conflict}
    <span class="tag marker-conflict" title={titleForMarker("merge_conflict")}>Conflict</span>
  {/if}
  {#if item.needs_rebase}
    <span class="tag marker-conflict" title={titleForMarker("needs_rebase")}>Rebase</span>
  {/if}
  {#if item.failing_checks && !readyButRed}
    <!-- failing_checks is a marker, not a state — never in item.states (ADR-0016) -->
    <span class="tag marker-conflict" title={titleForMarker("failing_checks")}>Failing Checks</span>
  {/if}
  {#if item.needs_approval}
    <span class="tag marker-conflict" title={titleForMarker("needs_approval")}>Missing Approvals</span>
  {/if}
  {#if item.unresolved_discussions}
    <span class="tag marker-conflict" title={titleForMarker("unresolved_discussions")}>Discussions</span>
  {/if}
  {#if item.policy_denied}
    <span class="tag marker-conflict" title={titleForMarker("policy_denied")}>Policy</span>
  {/if}

  {#if item.old || item.stale}
    <span class="tag marker-stale" title={item.old ? oldTitle : staleTitle}>Stale</span>
  {/if}
</span>

<style>
  .tags {
    display: flex;
    flex-wrap: wrap;
    gap: 4px;
    align-items: center;
  }
  .tag {
    font-size: 11px;
    font-weight: 500;
    padding: 1px 7px;
    border-radius: 10px;
    border: 1px solid currentColor;
    white-space: nowrap;
  }
  .marker-draft {
    color: var(--marker-draft);
    border-color: var(--marker-draft);
  }
  .marker-conflict {
    color: var(--marker-conflict);
    border-color: var(--marker-conflict);
  }
  .marker-stale {
    color: var(--marker-stale);
    border-color: var(--marker-stale);
  }
</style>
