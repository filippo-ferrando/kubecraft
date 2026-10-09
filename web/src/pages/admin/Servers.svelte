<script lang="ts">
  import { onMount } from "svelte";
  import { get, errorText } from "../../lib/api";
  import { navigate } from "../../lib/router.svelte";
  import { poll } from "../../lib/poll";
  import { typeLabels } from "../../lib/format";
  import type { Server } from "../../lib/types";
  import StatusBadge from "../../components/StatusBadge.svelte";
  import ServerActions from "../../components/ServerActions.svelte";

  let servers = $state<Server[]>([]);
  let owner = $state("");
  let error = $state("");

  onMount(() =>
    poll(async () => {
      try {
        servers = await get<Server[]>("/admin/servers");
        error = "";
      } catch (e) {
        error = errorText(e);
      }
    }, 5000),
  );

  const owners = $derived([...new Set(servers.map((s) => s.owner_username ?? ""))].sort());
  const shown = $derived(owner ? servers.filter((s) => s.owner_username === owner) : servers);
</script>

<div class="space-y-4">
  <div class="flex items-center gap-2">
    <label class="label mb-0" for="owner">Owner</label>
    <select id="owner" class="input w-auto" bind:value={owner}>
      <option value="">All</option>
      {#each owners as o}<option value={o}>{o}</option>{/each}
    </select>
    <span class="muted">{shown.length} servers</span>
  </div>
  {#if error}<p class="text-sm text-red-500">{error}</p>{/if}
  <div class="card overflow-x-auto p-0">
    <table class="w-full text-left text-sm">
      <thead class="border-b border-zinc-200 text-xs text-zinc-500 uppercase dark:border-zinc-800">
        <tr><th class="p-3">Server</th><th class="p-3">Owner</th><th class="p-3">Type</th><th class="p-3">Plan</th><th class="p-3">State</th><th class="p-3"></th></tr>
      </thead>
      <tbody class="divide-y divide-zinc-200 dark:divide-zinc-800">
        {#each shown as s (s.id)}
          <tr>
            <td class="p-3"><button class="font-medium hover:underline" onclick={() => navigate(`/servers/${s.id}`)}>{s.display_name}</button><div class="font-mono text-xs text-zinc-500">{s.address}</div></td>
            <td class="p-3">{s.owner_username}</td>
            <td class="p-3">{typeLabels[s.type] ?? s.type} {s.version}</td>
            <td class="p-3">{s.plan.name}</td>
            <td class="p-3"><StatusBadge status={s.status} /></td>
            <td class="p-3"><ServerActions server={s} compact onchange={(n) => (servers = servers.map((x) => (x.id === n.id ? { ...n, owner_username: x.owner_username } : x)))} /></td>
          </tr>
        {:else}
          <tr><td colspan="6" class="muted p-6 text-center">No servers.</td></tr>
        {/each}
      </tbody>
    </table>
  </div>
  <p class="muted">Open a server to delete it or use its console.</p>
</div>
