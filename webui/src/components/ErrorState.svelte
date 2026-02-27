<script lang="ts">
  let {
    message = "Something went wrong.",
    onretry,
  }: { message?: string; onretry?: () => void } = $props();
</script>

<div class="flex flex-col items-center justify-center py-16 gap-4">
  <div class="text-error text-4xl">⚠</div>
  <p class="text-base-content/70">{message}</p>
  {#if onretry}
    <button class="btn btn-sm btn-outline" onclick={onretry}>Retry</button>
  {/if}
</div>
