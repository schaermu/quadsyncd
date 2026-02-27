<script lang="ts">
  import { onMount } from "svelte";
  import { fetchUnits, type UnitInfo } from "../lib/api";
  import { shortSha } from "../lib/format";
  import LoadingState from "../components/LoadingState.svelte";
  import ErrorState from "../components/ErrorState.svelte";
  import EmptyState from "../components/EmptyState.svelte";

  let loading = $state(true);
  let error = $state<string | null>(null);
  let units = $state<UnitInfo[]>([]);

  async function load() {
    loading = true;
    error = null;
    try {
      const resp = await fetchUnits();
      units = resp.items;
    } catch (e) {
      error = e instanceof Error ? e.message : "Failed to load units";
    } finally {
      loading = false;
    }
  }

  onMount(() => {
    load();
  });
</script>

<div class="p-4 max-w-5xl mx-auto space-y-4">
  <h1 class="text-2xl font-bold">Managed Units</h1>
  <p class="text-sm text-base-content/60">
    Read-only view of Quadlet units currently managed by quadsyncd.
  </p>

  {#if loading}
    <LoadingState />
  {:else if error}
    <ErrorState message={error} onretry={load} />
  {:else if units.length === 0}
    <EmptyState message="No managed units found." />
  {:else}
    <div class="overflow-x-auto">
      <table class="table table-sm">
        <thead>
          <tr>
            <th>Unit Name</th>
            <th>Source Path</th>
            <th>Source Repo</th>
            <th>Ref</th>
            <th>SHA</th>
            <th>Hash</th>
          </tr>
        </thead>
        <tbody>
          {#each units as unit}
            <tr class="hover">
              <td class="font-mono text-xs font-medium">{unit.name}</td>
              <td class="font-mono text-xs">{unit.source_path}</td>
              <td class="text-xs max-w-[200px] truncate">
                {unit.source_repo ?? "—"}
              </td>
              <td class="font-mono text-xs">{unit.source_ref ?? "—"}</td>
              <td class="font-mono text-xs">{shortSha(unit.source_sha)}</td>
              <td class="font-mono text-xs">{shortSha(unit.hash)}</td>
            </tr>
          {/each}
        </tbody>
      </table>
    </div>
  {/if}
</div>
