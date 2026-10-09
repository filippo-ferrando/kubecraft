<script lang="ts">
  import { onMount } from "svelte";
  import { get, errorText } from "../../lib/api";
  import type { AuditEntry } from "../../lib/types";

  let entries = $state<AuditEntry[]>([]);
  let filter = $state("");
  let more = $state(true);
  let loading = $state(false);
  let error = $state("");

  async function load(before?: number) {
    loading = true;
    try {
      const page = await get<AuditEntry[]>(`/admin/audit?limit=100${before ? `&before=${before}` : ""}`);
      entries = before ? [...entries, ...page] : page;
      more = page.length === 100;
    } catch (e) {
      error = errorText(e);
    } finally {
      loading = false;
    }
  }
  onMount(() => load());

  const shown = $derived(
    filter ? entries.filter((e) => `${e.username} ${e.action} ${e.target} ${JSON.stringify(e.details)}`.toLowerCase().includes(filter.toLowerCase())) : entries,
  );

  function details(d: Record<string, unknown>) {
    return Object.entries(d)
      .filter(([k]) => k !== "ip")
      .map(([k, v]) => `${k}=${typeof v === "object" ? JSON.stringify(v) : v}`)
      .join(" ");
  }
</script>

<div class="space-y-4">
  <input class="input max-w-sm" placeholder="Filter loaded entries" bind:value={filter} />
  {#if error}<p class="text-sm text-red-500">{error}</p>{/if}
  <div class="card overflow-x-auto p-0">
    <table class="w-full text-left text-sm">
      <thead class="border-b border-zinc-200 text-xs text-zinc-500 uppercase dark:border-zinc-800">
        <tr><th class="p-3">Time</th><th class="p-3">User</th><th class="p-3">Action</th><th class="p-3">Target</th><th class="p-3">Details</th></tr>
      </thead>
      <tbody class="divide-y divide-zinc-200 dark:divide-zinc-800">
        {#each shown as e (e.id)}
          <tr>
            <td class="p-3 whitespace-nowrap text-zinc-500">{new Date(e.created_at).toLocaleString()}</td>
            <td class="p-3">{e.username || "-"}</td>
            <td class="p-3 font-mono text-xs">{e.action}</td>
            <td class="p-3">{e.target}</td>
            <td class="max-w-md truncate p-3 font-mono text-xs text-zinc-500" title={JSON.stringify(e.details)}>{details(e.details)}</td>
          </tr>
        {/each}
      </tbody>
    </table>
  </div>
  {#if more}
    <button class="btn-secondary" disabled={loading} onclick={() => load(entries.at(-1)?.id)}>{loading ? "Loading..." : "Load older"}</button>
  {/if}
</div>
