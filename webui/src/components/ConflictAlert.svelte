<script lang="ts">
  let { count }: { count: number } = $props();
</script>

{#if count > 0}
  <div class="alert alert-warning alert-soft">
    <span>{count} conflict{count > 1 ? "s" : ""} detected</span>
  </div>
{/if}
