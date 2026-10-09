<script lang="ts">
  import { onMount } from "svelte";
  import type { Component } from "svelte";
  import { get, errorText, ApiError } from "../lib/api";
  import { link } from "../lib/router.svelte";
  import { poll } from "../lib/poll";
  import { typeLabels } from "../lib/format";
  import type { Server } from "../lib/types";
  import StatusBadge from "../components/StatusBadge.svelte";
  import CopyAddress from "../components/CopyAddress.svelte";
  import ServerActions from "../components/ServerActions.svelte";
  import Overview from "./server/Overview.svelte";
  import SettingsTab from "./server/SettingsTab.svelte";
  import Console from "./server/Console.svelte";
  import Players from "./server/Players.svelte";
  import Backups from "./server/Backups.svelte";
  import Danger from "./server/Danger.svelte";
  import Content from "./server/Content.svelte";
  import Files from "./server/Files.svelte";
  import { contentKind } from "../lib/format";

  let { id, tab = "overview" }: { id: string; tab?: string } = $props();

  type Tab = { key: string; label: string; page: Component<any> };
  const allTabs: Tab[] = [
    { key: "overview", label: "Overview", page: Overview },
    { key: "console", label: "Console", page: Console },
    { key: "players", label: "Players", page: Players },
    { key: "content", label: "Mods", page: Content },
    { key: "settings", label: "Settings", page: SettingsTab },
    { key: "files", label: "Files", page: Files },
    { key: "backups", label: "Backups", page: Backups },
    { key: "danger", label: "Danger zone", page: Danger },
  ];
  let server = $state<Server | null>(null);

  // The add-ons tab is named after the server type and hidden for vanilla.
  const tabs = $derived(
    allTabs
      .filter((t) => t.key !== "content" || (server && contentKind(server.type)))
      .map((t) => (t.key === "content" && server && contentKind(server.type) === "plugin" ? { ...t, label: "Plugins" } : t)),
  );
  const current = $derived(tabs.find((t) => t.key === tab) ?? tabs[0]);
  let error = $state("");
  let notFound = $state(false);

  async function load() {
    try {
      server = await get<Server>(`/servers/${id}`);
      error = "";
    } catch (e) {
      if (e instanceof ApiError && e.status === 404) notFound = true;
      else error = errorText(e);
    }
  }

  onMount(() => poll(load, 3000));
</script>

{#if notFound}
  <div class="py-24 text-center">
    <h1 class="text-xl font-semibold">Server not found</h1>
    <a href="/" use:link class="mt-4 inline-block text-brand-500 hover:underline">Back to servers</a>
  </div>
{:else if !server}
  <p class="muted">{error || "Loading..."}</p>
{:else}
  <div class="space-y-6">
    <div class="flex flex-wrap items-start justify-between gap-4">
      <div class="min-w-0 space-y-2">
        <div class="flex items-center gap-3">
          <a href="/" use:link class="text-zinc-500 hover:text-zinc-900 dark:hover:text-zinc-100">←</a>
          <h1 class="truncate text-2xl font-semibold">{server.display_name}</h1>
          <StatusBadge status={server.status} />
        </div>
        <div class="flex flex-wrap items-center gap-2 text-sm text-zinc-500">
          <CopyAddress address={server.address} />
          {#if server.playit?.found}
            <span class="rounded bg-sky-500/15 px-1.5 py-0.5 text-xs font-medium text-sky-500" title="Public address through playit.gg">playit</span>
            <span class="text-xs">LAN/Tailscale: <span class="font-mono">{server.private_address}</span></span>
          {/if}
          <span>{typeLabels[server.type] ?? server.type} {server.version} · {server.plan.name}</span>
          {#if server.owner_username}<span>· owner {server.owner_username}</span>{/if}
        </div>
      </div>
      <ServerActions {server} onchange={(s) => (server = s)} />
    </div>

    {#if server.status.restart_required && server.desired_state === "running"}
      <p class="rounded-lg border border-amber-500/40 bg-amber-500/10 p-3 text-sm">Settings changed. Restart the server to apply them.</p>
    {/if}
    {#if error}<p class="text-sm text-red-500">{error}</p>{/if}

    <nav class="flex gap-1 overflow-x-auto border-b border-zinc-200 dark:border-zinc-800">
      {#each tabs as t (t.key)}
        <a
          href={`/servers/${id}/${t.key}`}
          use:link
          class="-mb-px shrink-0 border-b-2 px-3 py-2 text-sm {current.key === t.key
            ? 'border-brand-500 font-medium'
            : 'border-transparent text-zinc-500 hover:text-zinc-900 dark:hover:text-zinc-100'}">{t.label}</a
        >
      {/each}
    </nav>

    <current.page {server} onchange={(s: Server) => (server = s)} />
  </div>
{/if}
