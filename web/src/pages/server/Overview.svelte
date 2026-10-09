<script lang="ts">
  import type { Server } from "../../lib/types";
  import { cpu, mem, planSummary, stateStyle, uptime } from "../../lib/format";
  import ServerStats from "../../components/ServerStats.svelte";

  let { server }: { server: Server; onchange: (s: Server) => void } = $props();
  const s = $derived(server.status);
</script>

<div class="grid gap-4 md:grid-cols-2">
  <div class="card space-y-3">
    <h2 class="font-medium">Status</h2>
    <dl class="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1 text-sm">
      <dt class="text-zinc-500">State</dt>
      <dd class={stateStyle[s.state]?.text}>{stateStyle[s.state]?.label ?? s.state}</dd>
      {#if s.message}
        <dt class="text-zinc-500">Details</dt>
        <dd class="break-words">{s.message}</dd>
      {/if}
      {#if s.started_at && s.state === "running"}
        <dt class="text-zinc-500">Uptime</dt>
        <dd>{uptime(s.started_at)}</dd>
      {/if}
      {#if s.node}
        <dt class="text-zinc-500">Node</dt>
        <dd>{s.node}</dd>
      {/if}
    </dl>
    <ServerStats {server} />
  </div>
  <div class="card space-y-3">
    <h2 class="font-medium">Connect</h2>
    <p class="text-sm">In Minecraft, open <b>Multiplayer → Add Server</b> and use:</p>
    <p class="rounded-md bg-zinc-100 p-2 font-mono text-sm dark:bg-zinc-800">{server.address}</p>
    <p class="muted">Java Edition, default port 25565.</p>
  </div>
  <div class="card space-y-3">
    <h2 class="font-medium">Plan: {server.plan.name}</h2>
    <p class="text-sm">{planSummary(server.plan)}</p>
    <p class="muted">
      CPU guaranteed {cpu(server.plan.cpu_request_m)}, up to {cpu(server.plan.cpu_limit_m)}. Java heap is about 75% of
      {mem(server.plan.memory_limit_mb)}.
    </p>
  </div>
</div>
