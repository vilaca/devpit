<script lang="ts">
  const {
    label,
    cause,
    onDismiss,
    onViewLog,
  }: {
    label: string;
    cause: string;
    onDismiss: () => void;
    onViewLog: () => void;
  } = $props();
</script>

<div class="banner" role="alert">
  <span class="msg">
    Sync failed for <strong>{label}</strong>: {cause}
  </span>
  <div class="actions">
    <button class="btn-link" onclick={onViewLog}>View log</button>
    <button class="btn-dismiss" onclick={onDismiss} aria-label="Dismiss">✕</button>
  </div>
</div>

<style>
  .banner {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 12px;
    padding: 7px 16px;
    background: color-mix(in srgb, var(--health-failing) 10%, var(--bg));
    border-bottom: 1px solid color-mix(in srgb, var(--health-failing) 40%, transparent);
    font-size: 13px;
  }
  .msg {
    color: var(--text);
  }
  .actions {
    display: flex;
    align-items: center;
    gap: 10px;
    flex-shrink: 0;
  }
  .btn-link {
    border: none;
    background: none;
    color: var(--accent);
    font-size: 13px;
    padding: 0;
    text-decoration: underline;
  }
  .btn-dismiss {
    border: none;
    background: none;
    color: var(--text-faint);
    font-size: 13px;
    padding: 0 2px;
    line-height: 1;
  }
  .btn-dismiss:hover {
    color: var(--text);
  }
</style>
