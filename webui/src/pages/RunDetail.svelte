<script lang="ts">
  import { onDestroy } from "svelte";
  import {
    fetchRunDetail,
    fetchRunLogs,
    fetchRunPlan,
    type RunMeta,
    type LogEntry,
    type Plan,
  } from "../lib/api";
  import { onSSEEvent } from "../lib/sse";
  import {
    formatTimestamp,
    shortSha,
    levelPrefix,
    levelColor,
  } from "../lib/format";
  import StatusBadge from "../components/StatusBadge.svelte";
  import LoadingState from "../components/LoadingState.svelte";
  import ErrorState from "../components/ErrorState.svelte";
  import EmptyState from "../components/EmptyState.svelte";
  import ConflictAlert from "../components/ConflictAlert.svelte";
  import PlanOpsTable from "../components/PlanOpsTable.svelte";
  import { link } from "svelte-spa-router";

  let { params }: { params: { id: string } } = $props();

  const MAX_LOG_ENTRIES = 5000;

  let loading = $state(true);
  let error = $state<string | null>(null);
  let run = $state<RunMeta | null>(null);
  let logs = $state<LogEntry[]>([]);
  let plan = $state<Plan | null>(null);
  let activeTab = $state<"logs" | "plan" | "meta">("logs");
  let wrapLogs = $state(false);
  let levelFilter = $state("");
  let searchFilter = $state("");
  let abortController: AbortController | undefined;

  async function load() {
    abortController?.abort();
    abortController = new AbortController();
    const signal = abortController.signal;
    loading = true;
    error = null;
    try {
      const meta = await fetchRunDetail(params.id, signal);
      run = meta;
      const [logsResp, planResp] = await Promise.allSettled([
        fetchRunLogs(params.id, { limit: 500 }, signal),
        fetchRunPlan(params.id, signal),
      ]);
      logs =
        logsResp.status === "fulfilled" ? logsResp.value.items : [];
      plan = planResp.status === "fulfilled" ? planResp.value : null;
    } catch (e) {
      if (e instanceof Error && e.name === "AbortError") return;
      error = e instanceof Error ? e.message : "Failed to load run";
    } finally {
      loading = false;
    }
  }

  const filteredLogs = $derived.by(() => {
    return logs.filter((entry) => {
      if (levelFilter && entry.level?.toLowerCase() !== levelFilter)
        return false;
      if (
        searchFilter &&
        !entry.msg?.toLowerCase().includes(searchFilter.toLowerCase())
      )
        return false;
      return true;
    });
  });

  $effect(() => {
    const _id = params.id;
    load();

    const unsubscribe = onSSEEvent((kind, payload) => {
      if (payload.run_id !== _id) return;
      if (kind === "run_updated") {
        load();
      } else if (kind === "log_appended" && payload.lines) {
        const newLogs = [...logs, ...payload.lines];
        logs =
          newLogs.length > MAX_LOG_ENTRIES
            ? newLogs.slice(newLogs.length - MAX_LOG_ENTRIES)
            : newLogs;
      } else if (kind === "plan_ready") {
        fetchRunPlan(_id)
          .then((p) => (plan = p))
          .catch(() => {});
      }
    });

    return () => {
      unsubscribe();
    };
  });

  onDestroy(() => {
    abortController?.abort();
  });
</script>

<div class="p-4 max-w-6xl mx-auto space-y-4">
  <div class="flex items-center gap-2">
    <a href="/runs" use:link class="btn btn-ghost btn-sm">← Runs</a>
    <h1 class="text-xl font-bold font-mono">{params.id}</h1>
  </div>

  {#if loading}
    <LoadingState />
  {:else if error}
    <ErrorState message={error} onretry={load} />
  {:else if run}
    <!-- Tab navigation -->
    <div role="tablist" class="tabs tabs-bordered">
      <button
        id="tab-logs"
        role="tab"
        aria-selected={activeTab === "logs"}
        class="tab {activeTab === 'logs' ? 'tab-active' : ''}"
        onclick={() => (activeTab = "logs")}
      >
        Logs ({filteredLogs.length})
      </button>
      <button
        id="tab-plan"
        role="tab"
        aria-selected={activeTab === "plan"}
        class="tab {activeTab === 'plan' ? 'tab-active' : ''}"
        onclick={() => (activeTab = "plan")}
      >
        Plan {plan ? `(${plan.ops.length})` : ""}
      </button>
      <button
        id="tab-meta"
        role="tab"
        aria-selected={activeTab === "meta"}
        class="tab {activeTab === 'meta' ? 'tab-active' : ''}"
        onclick={() => (activeTab = "meta")}
      >
        Details
      </button>
    </div>

    <!-- Logs Tab -->
    {#if activeTab === "logs"}
      <div role="tabpanel" aria-labelledby="tab-logs">
      <div class="space-y-2">
        <!-- Filter bar -->
        <div class="flex flex-wrap gap-2 items-center">
          <select
            class="select select-sm select-bordered w-32"
            bind:value={levelFilter}
          >
            <option value="">All levels</option>
            <option value="debug">DEBUG</option>
            <option value="info">INFO</option>
            <option value="warn">WARN</option>
            <option value="error">ERROR</option>
          </select>
          <input
            type="text"
            placeholder="Search logs…"
            class="input input-sm input-bordered flex-1 min-w-[150px]"
            bind:value={searchFilter}
          />
          <label class="label cursor-pointer gap-1">
            <span class="label-text text-xs">Wrap</span>
            <input
              type="checkbox"
              class="toggle toggle-xs"
              bind:checked={wrapLogs}
            />
          </label>
        </div>

        <!-- Log output -->
        {#if filteredLogs.length === 0}
          <EmptyState message="No matching log entries." />
        {:else}
          <div
            class="bg-base-300 rounded-box p-3 overflow-x-auto max-h-[600px] overflow-y-auto"
          >
            <pre
              class="text-xs font-mono leading-relaxed {wrapLogs
                ? 'whitespace-pre-wrap break-all'
                : 'whitespace-pre'}"
            >{#each filteredLogs as entry, i}<span
                  class="log-line {levelColor(entry.level)}"
                  ><span class="text-base-content/40 select-none"
                    >{String(i + 1).padStart(4, " ")} </span
                  ><span class="font-semibold">[{levelPrefix(entry.level)}]</span
                  > {entry.msg ?? ""}{#if entry.error} error={entry.error}{/if}
</span>{/each}</pre>
          </div>
        {/if}
      </div>
      </div>
    {/if}

    <!-- Plan Tab -->
    {#if activeTab === "plan"}
      <div role="tabpanel" aria-labelledby="tab-plan">
      {#if !plan}
        <EmptyState message="No plan data available for this run." />
      {:else if plan.ops.length === 0}
        <div class="alert alert-info">
          <span>No changes detected – everything is in sync.</span>
        </div>
      {:else}
        <div class="space-y-3">
          <ConflictAlert count={plan.conflicts.length} />
          <PlanOpsTable ops={plan.ops} layout="table" />
        </div>
      {/if}
      </div>
    {/if}

    <!-- Meta/Details Tab -->
    {#if activeTab === "meta"}
      <div role="tabpanel" aria-labelledby="tab-meta">
      <div class="grid grid-cols-1 md:grid-cols-2 gap-4">
        <div class="card bg-base-200 shadow-sm">
          <div class="card-body p-4 space-y-2">
            <h3 class="font-semibold">Run Info</h3>
            <div class="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1 text-sm">
              <span class="text-base-content/60">Status</span>
              <StatusBadge status={run.status} />
              <span class="text-base-content/60">Kind</span>
              <span>{run.kind}{run.dry_run ? " (dry-run)" : ""}</span>
              <span class="text-base-content/60">Trigger</span>
              <span>{run.trigger}</span>
              <span class="text-base-content/60">Started</span>
              <span>{formatTimestamp(run.started_at)}</span>
              <span class="text-base-content/60">Ended</span>
              <span>{formatTimestamp(run.ended_at)}</span>
              {#if run.error}
                <span class="text-base-content/60">Error</span>
                <span class="text-error text-xs">{run.error}</span>
              {/if}
            </div>
          </div>
        </div>

        {#if Object.keys(run.revisions ?? {}).length > 0}
          <div class="card bg-base-200 shadow-sm">
            <div class="card-body p-4 space-y-2">
              <h3 class="font-semibold">Revisions</h3>
              <div class="space-y-1">
                {#each Object.entries(run.revisions) as [repo, sha]}
                  <div class="text-sm">
                    <span class="text-base-content/60 text-xs break-all"
                      >{repo}</span
                    >
                    <span class="font-mono text-xs ml-2"
                      >{shortSha(sha)}</span
                    >
                  </div>
                {/each}
              </div>
            </div>
          </div>
        {/if}

        {#if run.conflicts && run.conflicts.length > 0}
          <div class="card bg-base-200 shadow-sm md:col-span-2">
            <div class="card-body p-4 space-y-2">
              <h3 class="font-semibold text-warning">Conflicts</h3>
              <div class="overflow-x-auto">
                <table class="table table-xs">
                  <thead>
                    <tr>
                      <th scope="col">Merge Key</th>
                      <th scope="col">Winner</th>
                      <th scope="col">Losers</th>
                    </tr>
                  </thead>
                  <tbody>
                    {#each run.conflicts as conflict}
                      <tr>
                        <td class="font-mono text-xs">{conflict.merge_key}</td>
                        <td class="text-xs">
                          {conflict.winner.source_repo}@{shortSha(
                            conflict.winner.source_sha,
                          )}
                        </td>
                        <td class="text-xs">
                          {#each conflict.losers as loser, li}
                            {#if li > 0}, {/if}
                            {loser.source_repo}@{shortSha(loser.source_sha)}
                          {/each}
                        </td>
                      </tr>
                    {/each}
                  </tbody>
                </table>
              </div>
            </div>
          </div>
        {/if}
      </div>
      </div>
    {/if}
  {/if}
</div>
