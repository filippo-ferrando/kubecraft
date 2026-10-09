<script lang="ts">
  import type { Component } from "svelte";
  import { link } from "../lib/router.svelte";
  import Users from "./admin/Users.svelte";
  import Plans from "./admin/Plans.svelte";
  import Servers from "./admin/Servers.svelte";
  import Audit from "./admin/Audit.svelte";

  let { tab = "users" }: { tab?: string } = $props();
  const tabs: { key: string; label: string; page: Component<any> }[] = [
    { key: "users", label: "Users & quotas", page: Users },
    { key: "plans", label: "Plans", page: Plans },
    { key: "servers", label: "All servers", page: Servers },
    { key: "audit", label: "Audit log", page: Audit },
  ];
  const current = $derived(tabs.find((t) => t.key === tab) ?? tabs[0]);
</script>

<div class="space-y-6">
  <h1 class="text-2xl font-semibold">Admin</h1>
  <nav class="flex gap-1 overflow-x-auto border-b border-zinc-200 dark:border-zinc-800">
    {#each tabs as t (t.key)}
      <a
        href={`/admin/${t.key}`}
        use:link
        class="-mb-px shrink-0 border-b-2 px-3 py-2 text-sm {current.key === t.key
          ? 'border-brand-500 font-medium'
          : 'border-transparent text-zinc-500 hover:text-zinc-900 dark:hover:text-zinc-100'}">{t.label}</a
      >
    {/each}
  </nav>
  {#key current.key}
    <current.page />
  {/key}
</div>
