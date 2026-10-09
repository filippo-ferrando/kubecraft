<script lang="ts">
  import type { Server } from "../lib/types";
  import CopyAddress from "./CopyAddress.svelte";

  let { server }: { server: Server } = $props();
  const p = $derived(server.playit);
  const ready = $derived(!!p?.found && !!p.address);
</script>

{#if p}
  <div class="card space-y-3 md:col-span-2">
    <div class="flex items-center gap-2">
      <h2 class="font-medium">Public access (playit.gg)</h2>
      {#if p.found && p.online}
        <span class="rounded bg-brand-500/15 px-1.5 py-0.5 text-xs font-medium text-brand-500">online</span>
      {:else if p.problems?.length}
        <span class="rounded bg-red-500/15 px-1.5 py-0.5 text-xs font-medium text-red-500">problem</span>
      {:else}
        <span class="rounded bg-amber-500/15 px-1.5 py-0.5 text-xs font-medium text-amber-500">setting up</span>
      {/if}
    </div>

    {#if ready}
      <p class="text-sm">Friends join with:</p>
      <CopyAddress address={p.address ?? ""} />
    {:else if !p.problems?.length}
      <p class="text-sm">The tunnel is being created, the public address shows up here in about a minute.</p>
    {/if}
    {#if p.problems?.length}
      <ul class="list-disc space-y-1 pl-5 text-sm text-red-500">
        {#each p.problems as problem}<li>{problem}</li>{/each}
      </ul>
    {/if}
    <p class="muted">
      Tunnel {p.tunnel_name}{p.local_port ? `, local port ${p.local_port}` : ""}. LAN and Tailscale clients can also use {server.private_address}.
    </p>
  </div>
{/if}
