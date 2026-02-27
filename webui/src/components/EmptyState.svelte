<script lang="ts">
  let { message = "Nothing here yet." }: { message?: string } = $props();
</script>

<div class="flex flex-col items-center justify-center py-16 gap-4">
  <div class="text-base-content/30 text-4xl">∅</div>
  <p class="text-base-content/50">{message}</p>
</div>
