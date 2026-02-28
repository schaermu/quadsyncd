<script lang="ts">
  import EmptyState from "../components/EmptyState.svelte";
  import { link } from "svelte-spa-router";
</script>

<div class="p-4 max-w-5xl mx-auto space-y-4">
  <EmptyState message="Page not found." />
  <div class="flex justify-center">
    <a href="/" use:link class="btn btn-sm btn-outline">Go to Dashboard</a>
  </div>
</div>
