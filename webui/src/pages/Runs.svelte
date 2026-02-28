<script lang="ts">
  import { onMount, onDestroy } from "svelte";
  import { fetchRuns, fetchRunDetail, type RunMeta } from "../lib/api";
  import { onSSEEvent } from "../lib/sse";
  import { debounce } from "../lib/debounce";
  import { formatTimestamp, formatRelativeTime } from "../lib/format";
  import StatusBadge from "../components/StatusBadge.svelte";
  import LoadingState from "../components/LoadingState.svelte";
  import ErrorState from "../components/ErrorState.svelte";
  import EmptyState from "../components/EmptyState.svelte";
  import { link } from "svelte-spa-router";

  let loading = $state(true);
  let error = $state<string | null>(null);
  let runs = $state<RunMeta[]>([]);
  let nextCursor = $state<string | undefined>(undefined);
  let loadingMore = $state(false);
  let loadMoreError = $state<string | null>(null);
  let cleanup: (() => void) | undefined;

  async function load() {
    loading = true;
    error = null;
    try {
      const resp = await fetchRuns(20);
      runs = resp.items;
      nextCursor = resp.next_cursor;
    } catch (e) {
      error = e instanceof Error ? e.message : "Failed to load runs";
    } finally {
      loading = false;
    }
  }

  async function loadMore() {
    if (!nextCursor || loadingMore) return;
    loadingMore = true;
    loadMoreError = null;
    try {
      const resp = await fetchRuns(20, nextCursor);
      runs = [...runs, ...resp.items];
      nextCursor = resp.next_cursor;
    } catch (e) {
      loadMoreError = e instanceof Error ? e.message : "Failed to load more runs";
    } finally {
      loadingMore = false;
    }
  }

  const debouncedLoad = debounce(load, 500);

  onMount(() => {
    load();
    cleanup = onSSEEvent((kind, payload) => {
      if (kind === "run_started") {
        // New runs shift pagination offsets; reload to keep cursor in sync.
        debouncedLoad();
      } else if (kind === "run_updated" && payload.run_id) {
        fetchRunDetail(payload.run_id)
          .then((meta) => {
            runs = runs.map((r) => (r.id === meta.id ? meta : r));
          })
          .catch(() => {
            debouncedLoad();
          });
      }
    });
  });

  onDestroy(() => {
    cleanup?.();
    debouncedLoad.cancel();
  });
</script>

<div class="p-4 max-w-5xl mx-auto space-y-4">
  <h1 class="text-2xl font-bold">Runs</h1>

  {#if loading}
    <LoadingState />
  {:else if error}
    <ErrorState message={error} onretry={load} />
  {:else if runs.length === 0}
    <EmptyState message="No runs recorded yet." />
  {:else}
    <div class="overflow-x-auto">
      <table class="table table-sm">
        <thead>
          <tr>
            <th scope="col">ID</th>
            <th scope="col">Kind</th>
            <th scope="col">Status</th>
            <th scope="col">Trigger</th>
            <th scope="col">Started</th>
            <th scope="col">Ended</th>
          </tr>
        </thead>
        <tbody>
          {#each runs as run}
            <tr class="hover">
              <td>
                <a
                  href="/runs/{run.id}"
                  use:link
                  class="font-mono text-xs link link-primary"
                >
                  {run.id}
                </a>
              </td>
              <td>
                <span class="badge badge-xs badge-outline">{run.kind}</span>
                {#if run.dry_run}
                  <span class="badge badge-xs badge-ghost ml-1">dry-run</span>
                {/if}
              </td>
              <td><StatusBadge status={run.status} /></td>
              <td class="text-xs">{run.trigger}</td>
              <td class="text-xs" title={formatTimestamp(run.started_at)}>
                {formatRelativeTime(run.started_at)}
              </td>
              <td class="text-xs" title={formatTimestamp(run.ended_at)}>
                {run.ended_at ? formatRelativeTime(run.ended_at) : "—"}
              </td>
            </tr>
          {/each}
        </tbody>
      </table>
    </div>

    {#if nextCursor}
      <div class="flex flex-col items-center gap-1">
        <button
          class="btn btn-sm btn-outline"
          onclick={loadMore}
          disabled={loadingMore}
        >
          {#if loadingMore}
            <span class="loading loading-spinner loading-xs"></span>
          {/if}
          Load More
        </button>
        {#if loadMoreError}
          <p class="text-error text-sm text-center mt-1">{loadMoreError}</p>
        {/if}
      </div>
    {/if}
  {/if}
</div>
