<script lang="ts">
  import { stateStyle } from "../lib/format";
  import type { Status } from "../lib/types";

  let { status }: { status: Status } = $props();
  const s = $derived(stateStyle[status.state] ?? stateStyle.error);
</script>

<span class="inline-flex items-center gap-1.5 text-sm font-medium {s.text}" title={status.message}>
  <span class="h-2 w-2 rounded-full {s.dot}"></span>
  {s.label}
</span>
