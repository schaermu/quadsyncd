<script lang="ts">
  import { onMount, onDestroy } from "svelte";
  import {
    triggerPlan,
    fetchRunDetail,
    fetchRunPlan,
    type RunMeta,
    type Plan,
  } from "../lib/api";
  import { onSSEEvent } from "../lib/sse";
  import { shortSha } from "../lib/format";
  import StatusBadge from "../components/StatusBadge.svelte";
  import LoadingState from "../components/LoadingState.svelte";
  import ErrorState from "../components/ErrorState.svelte";
  import EmptyState from "../components/EmptyState.svelte";
  import { push } from "svelte-spa-router";

  let triggering = $state(false);
  let triggerError = $state<string | null>(null);
  let planRunId = $state<string | null>(null);
  let planRun = $state<RunMeta | null>(null);
  let plan = $state<Plan | null>(null);
  let loading = $state(false);
  let cleanup: (() => void) | undefined;

  async function handleTriggerPlan() {
    // Clear previous results
    plan = null;
    planRun = null;
    planRunId = null;
    triggerError = null;

    triggering = true;
    try {
      const resp = await triggerPlan();
      planRunId = resp.run_id;
      await loadPlanRun();
    } catch (e) {
      triggerError = e instanceof Error ? e.message : "Failed to trigger plan";
    } finally {
      triggering = false;
    }
  }

  async function loadPlanRun() {
    if (!planRunId) return;
    loading = true;
    try {
      const meta = await fetchRunDetail(planRunId);
      planRun = meta;
      if (meta.status !== "running") {
        try {
          plan = await fetchRunPlan(planRunId);
        } catch {
          plan = null;
        }
      }
    } catch {
      // will retry via SSE
    } finally {
      loading = false;
    }
  }

  onMount(() => {
    cleanup = onSSEEvent((kind, payload) => {
      if (!planRunId || payload.run_id !== planRunId) return;
      if (kind === "run_updated" || kind === "plan_ready") {
        loadPlanRun();
      }
    });
  });

  onDestroy(() => {
    cleanup?.();
  });
</script>

<div class="p-4 max-w-5xl mx-auto space-y-6">
  <h1 class="text-2xl font-bold">Plan</h1>

  <div class="card bg-base-200 shadow-sm">
    <div class="card-body p-4">
      <p class="text-sm text-base-content/70">
        Generate a dry-run plan to preview what changes would be applied.
        No files will be modified.
      </p>
      <div class="flex items-center gap-3 mt-2">
        <button
          class="btn btn-primary btn-sm"
          onclick={handleTriggerPlan}
          disabled={triggering || (planRun?.status === "running")}
        >
          {#if triggering}
            <span class="loading loading-spinner loading-xs"></span>
          {/if}
          Generate Plan
        </button>
        {#if planRunId}
          <button
            class="btn btn-ghost btn-xs"
            onclick={() => push(`/runs/${planRunId}`)}
          >
            View run details →
          </button>
        {/if}
      </div>
      {#if triggerError}
        <div class="alert alert-error mt-2">
          <span class="text-sm">{triggerError}</span>
        </div>
      {/if}
    </div>
  </div>

  {#if planRunId}
    {#if loading && !planRun}
      <LoadingState message="Loading plan results…" />
    {:else if planRun}
      <div class="flex items-center gap-2 text-sm">
        <StatusBadge status={planRun.status} />
        <span class="font-mono text-xs">{planRunId}</span>
        {#if planRun.status === "running"}
          <span class="loading loading-dots loading-xs"></span>
        {/if}
      </div>

      {#if planRun.status === "running"}
        <LoadingState message="Plan is being generated…" />
      {:else if planRun.error}
        <ErrorState message={planRun.error} />
      {:else if plan}
        {#if plan.ops.length === 0}
          <div class="alert alert-info">
            <span>No changes detected – everything is in sync.</span>
          </div>
        {:else}
          {#if plan.conflicts.length > 0}
            <div class="alert alert-warning">
              <span
                >{plan.conflicts.length} conflict{plan.conflicts.length > 1
                  ? "s"
                  : ""} detected</span
              >
            </div>
          {/if}

          <div class="space-y-4">
            <div class="text-sm text-base-content/60">
              {plan.ops.length} operation{plan.ops.length > 1 ? "s" : ""}
              planned
            </div>

            {#each plan.ops as op}
              <div class="card bg-base-300 shadow-sm">
                <div class="card-body p-3 space-y-1">
                  <div class="flex items-center gap-2">
                    <span
                      class="badge badge-sm {op.op === 'add'
                        ? 'badge-success'
                        : op.op === 'delete'
                          ? 'badge-error'
                          : 'badge-warning'}"
                    >
                      {op.op}
                    </span>
                    <span class="font-mono text-sm font-medium">{op.path}</span>
                  </div>
                  {#if op.source_repo}
                    <div class="text-xs text-base-content/50">
                      {op.source_repo}
                      {#if op.source_ref}@ {op.source_ref}{/if}
                      {#if op.source_sha}
                        ({shortSha(op.source_sha)}){/if}
                    </div>
                  {/if}
                </div>
              </div>
            {/each}
          </div>
        {/if}
      {:else}
        <EmptyState message="No plan data available." />
      {/if}
    {/if}
  {:else}
    <EmptyState message="Click 'Generate Plan' to preview changes." />
  {/if}
</div>
