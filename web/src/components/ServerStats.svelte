<script lang="ts">
  import type { Server } from "../lib/types";
  import { cpu, mem } from "../lib/format";
  import Meter from "./Meter.svelte";

  let { server }: { server: Server } = $props();
  const live = $derived(server.status.state === "running" ? server.live : undefined);
</script>

{#if live}
  <div class="space-y-2">
    {#if live.players}
      <p class="text-sm">
        <span class="font-medium">{live.players.online}</span>
        <span class="text-zinc-500">/ {live.players.max} players</span>
        {#if live.players.names.length}<span class="text-zinc-500"> · {live.players.names.slice(0, 3).join(", ")}{live.players.names.length > 3 ? "..." : ""}</span>{/if}
      </p>
    {/if}
    <div class="grid grid-cols-2 gap-3">
      {#if live.cpu_m !== undefined}
        <Meter label="CPU" value={live.cpu_m} max={server.plan.cpu_limit_m} text={`${cpu(live.cpu_m)} / ${cpu(server.plan.cpu_limit_m)}`} />
      {/if}
      {#if live.memory_mb !== undefined}
        <Meter label="RAM" value={live.memory_mb} max={server.plan.memory_limit_mb} text={`${mem(live.memory_mb)} / ${mem(server.plan.memory_limit_mb)}`} />
      {/if}
    </div>
  </div>
{:else if server.status.message && server.status.state !== "running"}
  <p class="line-clamp-2 text-xs text-zinc-500">{server.status.message}</p>
{/if}
