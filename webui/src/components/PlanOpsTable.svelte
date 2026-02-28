<script lang="ts">
  import type { PlanOp } from "../lib/api";
  import { shortSha } from "../lib/format";

  let {
    ops,
    layout = "table",
  }: {
    ops: PlanOp[];
    layout?: "table" | "cards";
  } = $props();

  function opBadgeClass(op: string): string {
    return op === "add"
      ? "badge-success"
      : op === "delete"
        ? "badge-error"
        : "badge-warning";
  }
</script>

{#if layout === "table"}
  <div class="overflow-x-auto">
    <table class="table table-sm">
      <thead>
        <tr>
          <th scope="col">Op</th>
          <th scope="col">Path</th>
          <th scope="col">Source</th>
          <th scope="col">Ref</th>
          <th scope="col">SHA</th>
        </tr>
      </thead>
      <tbody>
        {#each ops as op}
          <tr>
            <td><span class="badge badge-sm {opBadgeClass(op.op)}">{op.op}</span></td>
            <td class="font-mono text-xs">{op.path}</td>
            <td class="text-xs max-w-[200px] truncate">{op.source_repo ?? "—"}</td>
            <td class="font-mono text-xs">{op.source_ref ?? "—"}</td>
            <td class="font-mono text-xs">{shortSha(op.source_sha)}</td>
          </tr>
        {/each}
      </tbody>
    </table>
  </div>
{:else}
  <div class="space-y-4">
    <div class="text-sm text-base-content/60">
      {ops.length} operation{ops.length > 1 ? "s" : ""} planned
    </div>
    {#each ops as op}
      <div class="card bg-base-300 shadow-sm">
        <div class="card-body p-3 space-y-1">
          <div class="flex items-center gap-2">
            <span class="badge badge-sm {opBadgeClass(op.op)}">{op.op}</span>
            <span class="font-mono text-sm font-medium">{op.path}</span>
          </div>
          {#if op.source_repo}
            <div class="text-xs text-base-content/50">
              {op.source_repo}
              {#if op.source_ref}@ {op.source_ref}{/if}
              {#if op.source_sha} ({shortSha(op.source_sha)}){/if}
            </div>
          {/if}
        </div>
      </div>
    {/each}
  </div>
{/if}
