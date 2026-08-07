<script lang="ts">
  import type { AttentionItem, State } from "../lib/types";
  import {
    stateLabel,
    stateCSSVar,
    relativeTime,
    visibleStates,
  } from "../lib/format";

  const { item }: { item: AttentionItem } = $props();

  // A muted row (reviewed-done) suppresses its chips — except changes_requested
  // (ADR-0016). visibleStates encodes that rule; the draft/marker badges and the
  // stale tag are separately gated on !item.muted below.
  const chips = $derived(visibleStates(item.states, item.muted ?? false));

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
      const detail = item.gate_detail
        ? ` · provider says: ${item.gate_detail}`
        : "";
      return dur ? `${dur}${detail}` : detail || undefined;
    }
    if (s === "ready_to_merge" && item.failing_checks) {
      return dur
        ? `${dur} · a non-required check is red`
        : "a non-required check is red";
    }
    if (s === "mentioned") {
      return dur
        ? `${dur} · clears when the item closes`
        : "clears when the item closes";
    }
    return dur;
  }

  // Hover text for a diagnostic marker badge.
  function titleForMarker(key: string): string | undefined {
    const onset = item.since?.[key];
    return onset ? relativeTime(onset) : undefined;
  }

  // When ready_to_merge + failing_checks co-occur, the red checks are non-required:
  // the ready chip renders as usual and the failing-checks marker softens its label
  // to "optional checks red" (two badges, not one combined phrase).
  const readyButRed = $derived(
    item.states.includes("ready_to_merge") && item.failing_checks,
  );

  // Suppress the "Blocked" chip when a visible marker badge already names the
  // gate's reason (ADR-0016 amendment). Strict match on gate_detail only —
  // GitLab's needs_approval is true for nearly every unapproved MR, so any
  // looser rule would erase the chip even when the operative blocker is
  // something no marker shows (e.g. GitHub's opaque `mergeable_state: "blocked"`).
  type MarkerField =
    | "merge_conflict"
    | "needs_rebase"
    | "failing_checks"
    | "needs_approval"
    | "unresolved_discussions"
    | "policy_denied";
  const GATE_DETAIL_MARKER: Record<string, MarkerField> = {
    conflict: "merge_conflict",
    need_rebase: "needs_rebase",
    ci_must_pass: "failing_checks",
    not_approved: "needs_approval",
    discussions_not_resolved: "unresolved_discussions",
    policies_denied: "policy_denied",
    security_policy_violations: "policy_denied",
    dirty: "merge_conflict",
    behind: "needs_rebase",
    unstable: "failing_checks",
  };
  // Every mapped field renders its marker badge unconditionally (readyButRed only
  // relabels failing_checks, never hides it), so a suppressed `blocked` chip is
  // always backed by a visible marker naming the reason.
  const blockedSuppressed = $derived.by(() => {
    const marker = item.gate_detail
      ? GATE_DETAIL_MARKER[item.gate_detail]
      : undefined;
    return marker ? item[marker] : false;
  });

  const staleTitle = $derived(
    `No activity for ${relativeTime(item.updated_at)} (threshold: 7 days)`,
  );
  const oldTitle = $derived(
    `No activity for ${relativeTime(item.updated_at)} (threshold: 30 days)`,
  );
</script>

<span class="tags">
  {#if item.draft && !item.muted}
    <span class="tag marker-draft" title={titleForMarker("draft")}>Draft</span>
  {/if}

  {#each chips as s (s)}
    {#if s === "blocked" && blockedSuppressed}
      <!-- suppressed: the matching marker badge below already names the reason -->
    {:else if s === "checking"}
      <!-- gate unknown/transient — visual cue lives on the pin column in WorkItemRow -->
    {:else}
      <span
        class="tag"
        style:color={stateCSSVar(s)}
        style:border-color={stateCSSVar(s)}
        title={titleForState(s)}>{labelFor(s)}</span
      >
    {/if}
  {/each}

  <!-- Diagnostic markers and the stale tag are suppressed on muted rows, which
       carry only the changes_requested chip (see visibleStates). -->
  {#if !item.muted}
    {#if item.merge_conflict}
      <span class="tag marker-conflict" title={titleForMarker("merge_conflict")}
        >Conflict</span
      >
    {/if}
    {#if item.needs_rebase}
      <span class="tag marker-conflict" title={titleForMarker("needs_rebase")}
        >Rebase</span
      >
    {/if}
    {#if item.failing_checks}
      <!-- failing_checks is a marker, not a state — never in item.states (ADR-0016).
           When the item is ready_to_merge the red checks are non-required, so the
           badge softens its label to "optional checks red". -->
      <span class="tag marker-conflict" title={titleForMarker("failing_checks")}
        >{readyButRed ? "optional checks red" : "Failing Checks"}</span
      >
    {/if}
    {#if item.needs_approval}
      <span class="tag marker-conflict" title={titleForMarker("needs_approval")}
        >Missing Approvals</span
      >
    {/if}
    {#if item.unresolved_discussions}
      <span
        class="tag marker-conflict"
        title={titleForMarker("unresolved_discussions")}>Discussions</span
      >
    {/if}
    {#if item.policy_denied}
      <span class="tag marker-conflict" title={titleForMarker("policy_denied")}
        >Policy</span
      >
    {/if}

    {#if item.old || item.stale}
      <span class="tag marker-stale" title={item.old ? oldTitle : staleTitle}
        >Stale</span
      >
    {/if}
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
