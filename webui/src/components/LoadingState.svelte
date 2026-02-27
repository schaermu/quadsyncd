<script lang="ts">
  let { message = "Loading…" }: { message?: string } = $props();
</script>

<div class="flex flex-col items-center justify-center py-16 gap-4">
  <span class="loading loading-spinner loading-lg text-primary"></span>
  <p class="text-base-content/60">{message}</p>
</div>
