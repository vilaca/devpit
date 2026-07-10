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

  // Hover text for states/markers where the reason is derivable from the item.
  function titleFor(s: State): string | undefined {
    if (s === "blocked") {
      return item.failing_checks
        ? "Merge gate is not satisfied — failing checks or merge conflict"
        : "Merge gate is not satisfied (required reviews, branch protection, or other conditions)";
    }
    return undefined;
  }

  const staleTitle = $derived(
    `Stale — no activity for ${relativeTime(item.updated_at)} (threshold: 7 days)`,
  );
</script>

<span class="tags">
  {#if item.draft}
    <span class="tag marker-draft">Draft</span>
  {/if}
  {#each item.states as s (s)}
    <span
      class="tag"
      style:color={stateCSSVar(s)}
      style:border-color={stateCSSVar(s)}
      title={titleFor(s)}
    >{labelFor(s)}</span>
  {/each}
  {#if item.failing_checks}
    <!-- failing_checks is a marker, not a state — never in item.states (ADR-0016) -->
    <span class="tag marker-checks" title="Failing status checks or merge conflict (GitHub: unstable/dirty mergeable_state)">Checks failing</span>
  {/if}
  {#if item.stale}
    <span class="tag marker-stale" title={staleTitle}>Stale</span>
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
    opacity: 0.85;
  }
  .marker-draft {
    color: var(--marker-draft);
    border-color: var(--marker-draft);
  }
  .marker-checks {
    color: var(--marker-failing-checks);
    border-color: var(--marker-failing-checks);
  }
  .marker-stale {
    color: var(--marker-stale);
    border-color: var(--marker-stale);
  }
</style>
