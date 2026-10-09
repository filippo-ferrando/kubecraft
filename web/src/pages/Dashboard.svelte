<script lang="ts">
  import { onMount } from "svelte";
  import { get, errorText } from "../lib/api";
  import { link, navigate } from "../lib/router.svelte";
  import { poll } from "../lib/poll";
  import { planSummary, typeLabels } from "../lib/format";
  import type { Quota, Server } from "../lib/types";
  import StatusBadge from "../components/StatusBadge.svelte";
  import CopyAddress from "../components/CopyAddress.svelte";
  import QuotaBar from "../components/QuotaBar.svelte";
  import ServerActions from "../components/ServerActions.svelte";
  import ServerStats from "../components/ServerStats.svelte";

  let servers = $state<Server[] | null>(null);
  let quota = $state<Quota | null>(null);
  let error = $state("");

  async function load() {
    try {
      [servers, quota] = await Promise.all([get<Server[]>("/servers"), get<Quota>("/me/quota")]);
      error = "";
    } catch (e) {
      error = errorText(e);
    }
  }

  onMount(() => poll(load, 4000));

  const full = $derived(quota !== null && quota.limits.max_servers !== null && quota.usage.servers >= quota.limits.max_servers);

  function replace(s: Server) {
    servers = servers?.map((x) => (x.id === s.id ? s : x)) ?? null;
    get<Quota>("/me/quota").then((q) => (quota = q));
  }
</script>

<div class="space-y-6">
  <div class="flex flex-wrap items-center justify-between gap-3">
    <h1 class="text-2xl font-semibold">Servers</h1>
    <a href="/servers/new" use:link class="btn-primary {full ? 'pointer-events-none opacity-50' : ''}" aria-disabled={full}>+ New server</a>
  </div>

  {#if quota}<QuotaBar {quota} />{/if}
  {#if error}<p class="text-sm text-red-500">{error}</p>{/if}

  {#if servers === null}
    <p class="muted">Loading...</p>
  {:else if servers.length === 0}
    <div class="card flex flex-col items-center gap-3 py-12 text-center">
      <div class="text-4xl">⛏</div>
      <h2 class="text-lg font-medium">No servers yet</h2>
      <p class="muted max-w-sm">Create a server, pick a type and a plan, and it will be online in a couple of minutes.</p>
      {#if !full}<a href="/servers/new" use:link class="btn-primary">Create a server</a>{/if}
    </div>
  {:else}
    <div class="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
      {#each servers as s (s.id)}
        <div
          role="link"
          tabindex="0"
          class="card flex cursor-pointer flex-col gap-3 transition hover:border-brand-500/60"
          onclick={() => navigate(`/servers/${s.id}`)}
          onkeydown={(e) => e.key === "Enter" && navigate(`/servers/${s.id}`)}
        >
          <div class="flex items-start justify-between gap-2">
            <div class="min-w-0">
              <h2 class="truncate font-medium">{s.display_name}</h2>
              <p class="muted">{typeLabels[s.type] ?? s.type} {s.version} · {s.plan.name}</p>
            </div>
            <StatusBadge status={s.status} />
          </div>
          <CopyAddress address={s.address} />
          {#if s.playit?.problems?.length}
            <p class="text-xs text-red-500">playit tunnel has a problem, see the server page.</p>
          {:else if s.playit && !s.playit.found}
            <p class="text-xs text-amber-500">Public tunnel is being created.</p>
          {:else if s.playit && !s.playit.online}
            <p class="text-xs text-red-500">playit tunnel has a problem, see the server page.</p>
          {/if}
          <ServerStats server={s} />
          <p class="text-xs text-zinc-500">{planSummary(s.plan)}</p>
          <!-- svelte-ignore a11y_click_events_have_key_events, a11y_no_static_element_interactions -->
          <div onclick={(e) => e.stopPropagation()}>
            <ServerActions server={s} onchange={replace} compact />
          </div>
        </div>
      {/each}
    </div>
  {/if}
</div>
