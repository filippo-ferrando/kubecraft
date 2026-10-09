<script lang="ts">
  import { post, errorText } from "../../lib/api";
  import type { Server } from "../../lib/types";

  let { server, onchange }: { server: Server; onchange: (s: Server) => void } = $props();

  let wlName = $state("");
  let opName = $state("");
  let busy = $state("");
  let error = $state("");
  let notice = $state("");

  const online = $derived(server.status.state === "running" ? (server.live?.players ?? null) : null);
  const running = $derived(server.desired_state === "running");
  const valid = (n: string) => /^[A-Za-z0-9_]{3,16}$/.test(n.trim());

  async function act(action: string, name: string) {
    busy = action + name;
    error = notice = "";
    try {
      const r = await post<{ server: Server; applied_live: boolean }>(`/servers/${server.id}/players`, { action, name: name.trim() });
      onchange(r.server);
      if (running && !r.applied_live && action !== "kick") notice = "Saved. It applies when the server is reachable again.";
      return true;
    } catch (e) {
      error = errorText(e);
      return false;
    } finally {
      busy = "";
    }
  }
</script>

<div class="grid gap-4 md:grid-cols-2">
  <div class="card space-y-3 md:col-span-2">
    <h2 class="font-medium">Online now</h2>
    {#if !running}
      <p class="muted">The server is stopped.</p>
    {:else if !online}
      <p class="muted">Waiting for the server to report players...</p>
    {:else if online.names.length === 0}
      <p class="muted">Nobody is online ({online.online}/{online.max}).</p>
    {:else}
      <ul class="divide-y divide-zinc-200 dark:divide-zinc-800">
        {#each online.names as n}
          <li class="flex items-center justify-between py-2">
            <span class="font-mono text-sm">{n}</span>
            <span class="flex gap-2">
              {#if !server.settings.ops.some((o) => o.toLowerCase() === n.toLowerCase())}
                <button class="btn-secondary" disabled={!!busy} onclick={() => act("op", n)}>Make op</button>
              {/if}
              <button class="btn-secondary" disabled={!!busy} onclick={() => act("kick", n)}>Kick</button>
            </span>
          </li>
        {/each}
      </ul>
    {/if}
  </div>

  <div class="card space-y-3">
    <div class="flex items-center justify-between">
      <h2 class="font-medium">Whitelist</h2>
      <span class="text-xs {server.settings.whitelist_enabled ? 'text-brand-500' : 'text-zinc-500'}">
        {server.settings.whitelist_enabled ? "enforced" : "off (enable it in Settings)"}
      </span>
    </div>
    <form
      class="flex gap-2"
      onsubmit={async (e) => {
        e.preventDefault();
        if (await act("whitelist_add", wlName)) wlName = "";
      }}
    >
      <input class="input font-mono" placeholder="Player name" maxlength="16" bind:value={wlName} />
      <button class="btn-primary" disabled={!valid(wlName) || !!busy}>Add</button>
    </form>
    <ul class="divide-y divide-zinc-200 dark:divide-zinc-800">
      {#each server.settings.whitelist as n}
        <li class="flex items-center justify-between py-1.5">
          <span class="font-mono text-sm">{n}</span>
          <button class="text-sm text-zinc-500 hover:text-red-500" disabled={!!busy} onclick={() => act("whitelist_remove", n)}>Remove</button>
        </li>
      {:else}
        <li class="muted py-1.5">No players.</li>
      {/each}
    </ul>
  </div>

  <div class="card space-y-3">
    <h2 class="font-medium">Operators</h2>
    <form
      class="flex gap-2"
      onsubmit={async (e) => {
        e.preventDefault();
        if (await act("op", opName)) opName = "";
      }}
    >
      <input class="input font-mono" placeholder="Player name" maxlength="16" bind:value={opName} />
      <button class="btn-primary" disabled={!valid(opName) || !!busy}>Add</button>
    </form>
    <ul class="divide-y divide-zinc-200 dark:divide-zinc-800">
      {#each server.settings.ops as n}
        <li class="flex items-center justify-between py-1.5">
          <span class="font-mono text-sm">{n}</span>
          <button class="text-sm text-zinc-500 hover:text-red-500" disabled={!!busy} onclick={() => act("deop", n)}>Remove</button>
        </li>
      {:else}
        <li class="muted py-1.5">No operators.</li>
      {/each}
    </ul>
    <p class="muted">Ops added with /op in the console are reset on restart, manage them here.</p>
  </div>

  {#if error}<p class="text-sm text-red-500 md:col-span-2">{error}</p>{/if}
  {#if notice}<p class="text-sm text-amber-500 md:col-span-2">{notice}</p>{/if}
</div>
