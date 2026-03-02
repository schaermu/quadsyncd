<script lang="ts">
  let {
    message = "Something went wrong.",
    onretry,
  }: { message?: string; onretry?: () => void } = $props();
</script>

<div class="alert alert-error alert-soft flex-col items-center py-8 text-center">
  <div class="text-3xl">⚠</div>
  <p class="text-sm">{message}</p>
  {#if onretry}
    <button class="btn btn-sm btn-outline btn-error" onclick={onretry}>
      Retry
    </button>
  {/if}
</div>
