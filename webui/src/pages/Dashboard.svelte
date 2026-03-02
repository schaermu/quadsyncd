<script lang="ts">
  import { onMount, onDestroy } from "svelte";
  import {
    fetchOverview,
    fetchRuns,
    fetchTimer,
    type OverviewResponse,
    type RunMeta,
    type TimerInfo,
  } from "../lib/api";
  import { onSSEEvent } from "../lib/sse";
  import { debounce } from "../lib/debounce";
  import { formatTimestamp, formatRelativeTime, shortSha } from "../lib/format";
  import StatusBadge from "../components/StatusBadge.svelte";
  import LoadingState from "../components/LoadingState.svelte";
  import ErrorState from "../components/ErrorState.svelte";
  import EmptyState from "../components/EmptyState.svelte";
  import { link } from "svelte-spa-router";

  let loading = $state(true);
  let error = $state<string | null>(null);
  let overview = $state<OverviewResponse | null>(null);
  let recentRuns = $state<RunMeta[]>([]);
  let timer = $state<TimerInfo | null>(null);
  let cleanup: (() => void) | undefined;

  async function load() {
    loading = true;
    error = null;
    try {
      const [ov, runs, ti] = await Promise.all([
        fetchOverview(),
        fetchRuns(5),
        fetchTimer(),
      ]);
      overview = ov;
      recentRuns = runs.items;
      timer = ti;
    } catch (e) {
      error = e instanceof Error ? e.message : "Failed to load dashboard";
    } finally {
      loading = false;
    }
  }

  const debouncedLoad = debounce(load, 500);

  onMount(() => {
    load();
    cleanup = onSSEEvent((kind) => {
      if (kind === "run_started" || kind === "run_updated") {
        debouncedLoad();
      }
    });
  });

  onDestroy(() => {
    cleanup?.();
    debouncedLoad.cancel();
  });
</script>

<div class="page-shell page-stack">
  <div class="page-head">
    <h1 class="page-title">Dashboard</h1>
    <p class="page-subtitle">
      Live overview of sync activity, repository state, and recent runs.
    </p>
  </div>

  {#if loading}
    <LoadingState />
  {:else if error}
    <ErrorState message={error} onretry={load} />
  {:else}
    <div class="stats stats-vertical lg:stats-horizontal surface-card">
      <div class="stat">
        <div class="stat-title">Last Run</div>
        {#if overview?.last_run_id}
          <div class="stat-value text-lg sm:text-xl">
            <a
              href="/runs/{overview.last_run_id}"
              use:link
              class="font-mono link link-primary"
            >
              {overview.last_run_id}
            </a>
          </div>
          <div class="stat-desc mt-2">
            <StatusBadge status={overview.last_run_status ?? "unknown"} />
          </div>
        {:else}
          <div class="stat-value text-lg">—</div>
          <div class="stat-desc">No runs yet</div>
        {/if}
      </div>

      <div class="stat">
        <div class="stat-title">Sync Timer</div>
        <div class="stat-value text-lg sm:text-xl">
          {timer?.active ? "Active" : "Inactive"}
        </div>
        <div class="stat-desc font-mono text-xs">
          {timer?.unit ?? "Unknown"}
        </div>
      </div>

      <div class="stat">
        <div class="stat-title">Repositories</div>
        <div class="stat-value text-lg sm:text-xl">
          {overview?.repositories?.length ?? 0}
        </div>
        <div class="stat-desc">
          <span class="badge badge-ghost badge-sm">configured sources</span>
        </div>
      </div>
    </div>

    {#if overview?.repositories && overview.repositories.length > 0}
      <div class="flex items-center justify-between gap-2">
        <h2 class="card-title text-base">Tracked Repositories</h2>
        <span class="badge badge-outline badge-sm">
          {overview.repositories.length}
        </span>
      </div>
      <div class="table-shell overflow-x-auto">
        <table class="table table-sm table-zebra">
          <thead>
            <tr>
              <th scope="col">URL</th>
              <th scope="col">Ref</th>
              <th scope="col">Current SHA</th>
            </tr>
          </thead>
          <tbody>
            {#each overview.repositories as repo}
              <tr>
                <td class="font-mono text-xs break-all">{repo.url}</td>
                <td class="font-mono text-xs">{repo.ref ?? "—"}</td>
                <td class="font-mono text-xs">{shortSha(repo.sha)}</td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
    {/if}
    <div class="flex items-center justify-between">
      <h2 class="card-title text-base">Recent Runs</h2>
      <a href="/runs" use:link class="btn btn-sm btn-ghost">View all →</a>
    </div>
    {#if recentRuns.length === 0}
      <EmptyState message="No runs recorded yet." />
    {:else}
      <div class="table-shell overflow-x-auto">
        <table class="table table-sm table-zebra">
          <thead>
            <tr>
              <th scope="col">ID</th>
              <th scope="col">Kind</th>
              <th scope="col">Status</th>
              <th scope="col">Trigger</th>
              <th scope="col">Started</th>
            </tr>
          </thead>
          <tbody>
            {#each recentRuns as run}
              <tr>
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
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
    {/if}
  {/if}
</div>
