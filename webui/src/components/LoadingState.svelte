<script lang="ts">
  let { message = "Loading…" }: { message?: string } = $props();
</script>

<div
  class="card surface-card mx-auto max-w-md"
  role="status"
  aria-live="polite"
>
  <div class="card-body items-center py-10 text-center">
    <span class="loading loading-spinner loading-lg text-primary"></span>
    <p class="text-sm text-base-content/70">{message}</p>
  </div>
</div>
