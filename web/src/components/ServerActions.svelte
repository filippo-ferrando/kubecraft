<script lang="ts">
  import { post, errorText } from "../lib/api";
  import type { Server } from "../lib/types";

  let { server, onchange, compact = false }: { server: Server; onchange: (s: Server) => void; compact?: boolean } = $props();
  let busy = $state("");
  let error = $state("");

  async function act(action: "start" | "stop" | "restart") {
    busy = action;
    error = "";
    try {
      onchange(await post<Server>(`/servers/${server.id}/${action}`));
    } catch (e) {
      error = errorText(e);
    } finally {
      busy = "";
    }
  }

  const running = $derived(server.desired_state === "running");
</script>

<div class="flex flex-wrap items-center gap-2">
  {#if server.desired_state === "deleting"}
    <span class="muted">Deleting...</span>
  {:else if running && server.status.state === "sleeping"}
    <button class="btn-primary" disabled={!!busy} onclick={() => act("start")}>{busy === "start" ? "Waking up..." : "Wake up"}</button>
    <button class="btn-secondary" disabled={!!busy} onclick={() => act("stop")}>{busy === "stop" ? "Stopping..." : "Stop"}</button>
  {:else if running}
    <button class="btn-secondary" disabled={!!busy} onclick={() => act("stop")}>{busy === "stop" ? "Stopping..." : "Stop"}</button>
    {#if !compact}
      <button class="btn-secondary" disabled={!!busy} onclick={() => act("restart")}>
        {busy === "restart" ? "Restarting..." : "Restart"}
      </button>
    {/if}
  {:else}
    <button class="btn-primary" disabled={!!busy} onclick={() => act("start")}>{busy === "start" ? "Starting..." : "Start"}</button>
  {/if}
  {#if error}<span class="text-sm text-red-500">{error}</span>{/if}
</div>
