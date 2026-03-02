<script lang="ts">
  let { message = "Nothing here yet." }: { message?: string } = $props();
</script>

<div class="card border border-dashed border-base-content/20 bg-base-200/60">
  <div class="card-body items-center py-10 text-center">
    <div class="text-base-content/40 text-4xl">∅</div>
    <p class="text-sm text-base-content/60">{message}</p>
  </div>
</div>
