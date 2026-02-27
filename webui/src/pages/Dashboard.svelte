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

  onMount(() => {
    load();
    cleanup = onSSEEvent((kind) => {
      if (kind === "run_started" || kind === "run_updated") {
        load();
      }
    });
  });

  onDestroy(() => {
    cleanup?.();
  });
</script>

<div class="p-4 max-w-5xl mx-auto space-y-6">
  <h1 class="text-2xl font-bold">Dashboard</h1>

  {#if loading}
    <LoadingState />
  {:else if error}
    <ErrorState message={error} onretry={load} />
  {:else}
    <!-- Overview Cards -->
    <div class="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-4">
      <!-- Last Run -->
      <div class="card bg-base-200 shadow-sm">
        <div class="card-body p-4">
          <h2 class="card-title text-sm font-medium text-base-content/60">
            Last Run
          </h2>
          {#if overview?.last_run_id}
            <div class="flex items-center gap-2">
              <StatusBadge status={overview.last_run_status ?? "unknown"} />
              <a
                href="/runs/{overview.last_run_id}"
                use:link
                class="font-mono text-sm link link-primary"
              >
                {overview.last_run_id}
              </a>
            </div>
          {:else}
            <p class="text-sm text-base-content/50">No runs yet</p>
          {/if}
        </div>
      </div>

      <!-- Timer -->
      <div class="card bg-base-200 shadow-sm">
        <div class="card-body p-4">
          <h2 class="card-title text-sm font-medium text-base-content/60">
            Sync Timer
          </h2>
          {#if timer}
            <div class="flex items-center gap-2">
              <span
                class="badge badge-sm {timer.active
                  ? 'badge-success'
                  : 'badge-neutral'}"
              >
                {timer.active ? "active" : "inactive"}
              </span>
              <span class="text-sm font-mono">{timer.unit}</span>
            </div>
          {:else}
            <p class="text-sm text-base-content/50">Unknown</p>
          {/if}
        </div>
      </div>

      <!-- Repositories -->
      <div class="card bg-base-200 shadow-sm">
        <div class="card-body p-4">
          <h2 class="card-title text-sm font-medium text-base-content/60">
            Repositories
          </h2>
          <p class="text-2xl font-bold">
            {overview?.repositories?.length ?? 0}
          </p>
        </div>
      </div>
    </div>

    <!-- Repositories detail -->
    {#if overview?.repositories && overview.repositories.length > 0}
      <div class="card bg-base-200 shadow-sm">
        <div class="card-body p-4">
          <h2 class="text-lg font-semibold">Tracked Repositories</h2>
          <div class="overflow-x-auto">
            <table class="table table-sm">
              <thead>
                <tr>
                  <th>URL</th>
                  <th>Ref</th>
                  <th>Current SHA</th>
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
        </div>
      </div>
    {/if}

    <!-- Recent Runs -->
    <div class="card bg-base-200 shadow-sm">
      <div class="card-body p-4">
        <div class="flex items-center justify-between">
          <h2 class="text-lg font-semibold">Recent Runs</h2>
          <a href="/runs" use:link class="btn btn-ghost btn-xs">View all →</a>
        </div>
        {#if recentRuns.length === 0}
          <EmptyState message="No runs recorded yet." />
        {:else}
          <div class="overflow-x-auto">
            <table class="table table-sm">
              <thead>
                <tr>
                  <th>ID</th>
                  <th>Kind</th>
                  <th>Status</th>
                  <th>Trigger</th>
                  <th>Started</th>
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
                      <span class="badge badge-xs badge-outline">{run.kind}</span
                      >
                      {#if run.dry_run}
                        <span class="badge badge-xs badge-ghost ml-1"
                          >dry-run</span
                        >
                      {/if}
                    </td>
                    <td><StatusBadge status={run.status} /></td>
                    <td class="text-xs">{run.trigger}</td>
                    <td
                      class="text-xs"
                      title={formatTimestamp(run.started_at)}
                    >
                      {formatRelativeTime(run.started_at)}
                    </td>
                  </tr>
                {/each}
              </tbody>
            </table>
          </div>
        {/if}
      </div>
    </div>
  {/if}
</div>
