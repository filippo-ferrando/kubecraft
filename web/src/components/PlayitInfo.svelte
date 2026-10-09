<script lang="ts">
  import type { Server } from "../lib/types";
  import CopyAddress from "./CopyAddress.svelte";

  let { server }: { server: Server } = $props();
  const p = $derived(server.playit);
</script>

{#if p}
  <div class="card space-y-3 md:col-span-2">
    <div class="flex items-center gap-2">
      <h2 class="font-medium">Public access (playit.gg)</h2>
      {#if p.found && p.online}
        <span class="rounded bg-brand-500/15 px-1.5 py-0.5 text-xs font-medium text-brand-500">online</span>
      {:else if p.found}
        <span class="rounded bg-red-500/15 px-1.5 py-0.5 text-xs font-medium text-red-500">problem</span>
      {:else}
        <span class="rounded bg-amber-500/15 px-1.5 py-0.5 text-xs font-medium text-amber-500">no tunnel</span>
      {/if}
    </div>

    {#if !p.found}
      <p class="text-sm">Create the tunnel once in the playit.gg dashboard, kubecraft picks it up within a minute:</p>
      <ol class="list-decimal space-y-1 pl-5 text-sm">
        <li>Add a tunnel of type <b>Minecraft Java</b>, assigned to the kubecraft agent.</li>
        <li>Name: <CopyAddress address={p.tunnel_name} /></li>
        <li>Local address: <CopyAddress address={p.local_ip} /></li>
        <li>Local port: <CopyAddress address={String(p.suggested_port ?? "")} /> <span class="muted">(any free port above 1023, one per tunnel)</span></li>
      </ol>
      <p class="muted">Until then the server is reachable at {server.private_address} only.</p>
    {:else}
      <p class="text-sm">Friends join with:</p>
      <CopyAddress address={p.address ?? ""} />
      <p class="muted">
        Tunnel {p.tunnel_name}, local port {p.local_port}. LAN and Tailscale clients can also use {server.private_address}.
      </p>
      {#if p.problems?.length}
        <ul class="list-disc space-y-1 pl-5 text-sm text-red-500">
          {#each p.problems as problem}<li>{problem}</li>{/each}
        </ul>
      {/if}
    {/if}
  </div>
{/if}
