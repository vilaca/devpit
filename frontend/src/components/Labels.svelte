<script lang="ts">
  const { labels }: { labels: string[] } = $props();
</script>

<div class="labels">
  {#each labels as name (name)}
    <span class="label" title={name}>{name}</span>
  {/each}
</div>

<style>
  .labels {
    display: flex;
    flex-wrap: wrap;
    gap: 4px;
    align-items: center;
  }
  .label {
    font-size: 11px;
    font-weight: 500;
    white-space: nowrap;
    max-width: 100%;
    overflow: hidden;
    text-overflow: ellipsis;
    color: var(--text-muted);
  }
  .label::before {
    content: "#";
    opacity: 0.5;
  }
</style>
