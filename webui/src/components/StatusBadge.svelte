<script lang="ts">
  let { status }: { status: string } = $props();
  import { statusColor } from "../lib/format";
</script>

<span class="badge badge-sm badge-soft font-medium {statusColor(status)}">
  {status}
</span>
