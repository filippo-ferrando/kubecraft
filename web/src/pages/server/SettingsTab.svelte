<script lang="ts">
  import { onMount, untrack } from "svelte";
  import { get, patch, errorText } from "../../lib/api";
  import { isAdmin } from "../../lib/session.svelte";
  import { planSummary } from "../../lib/format";
  import type { Meta, Plan, Server, Settings } from "../../lib/types";
  import SettingsForm from "../../components/SettingsForm.svelte";

  let { server, onchange }: { server: Server; onchange: (s: Server) => void } = $props();

  let meta = $state<Meta | null>(null);
  let plans = $state<Plan[]>([]);
  // Copied once so polling does not overwrite edits in progress.
  const initial = untrack(() => $state.snapshot(server));
  let settings = $state<Settings>(initial.settings);
  let displayName = $state(initial.display_name);
  let version = $state(initial.version);
  let planId = $state(initial.plan.id);
  let saving = $state(false);
  let error = $state("");
  let saved = $state(false);

  onMount(async () => {
    [meta, plans] = await Promise.all([get<Meta>("/meta"), get<Plan[]>("/plans")]);
  });

  async function save(e: SubmitEvent) {
    e.preventDefault();
    saving = true;
    error = "";
    saved = false;
    try {
      const body: Record<string, unknown> = { settings, display_name: displayName, version };
      if (planId !== server.plan.id) body.plan_id = planId;
      onchange(await patch<Server>(`/servers/${server.id}`, body));
      saved = true;
    } catch (err) {
      error = errorText(err);
    } finally {
      saving = false;
    }
  }
</script>

{#if meta}
  <form class="space-y-4" onsubmit={save}>
    <div class="card grid gap-4 sm:grid-cols-2">
      <div>
        <label class="label" for="name">Display name</label>
        <input id="name" class="input" maxlength="64" bind:value={displayName} />
      </div>
      <div>
        <label class="label" for="version">Version</label>
        <input id="version" class="input font-mono" bind:value={version} />
        <p class="muted mt-1">LATEST or a version like 1.21.4. Downgrades can break worlds.</p>
      </div>
      <div class="sm:col-span-2">
        <label class="label" for="plan">Plan</label>
        <select id="plan" class="input" bind:value={planId}>
          {#each plans as p (p.id)}
            <option value={p.id} disabled={p.storage_gb < server.plan.storage_gb}>{p.name}: {planSummary(p)}</option>
          {/each}
        </select>
        <p class="muted mt-1">
          Plans with less disk than the current one are not possible.{isAdmin() ? "" : " Upgrades must fit your quota."}
        </p>
      </div>
    </div>
    <div class="card">
      <SettingsForm bind:settings {meta} />
    </div>
    <div class="flex items-center gap-3">
      <button class="btn-primary" disabled={saving}>{saving ? "Saving..." : "Save"}</button>
      {#if saved}<span class="text-sm text-brand-500">Saved. Restart the server to apply.</span>{/if}
      {#if error}<span class="text-sm text-red-500">{error}</span>{/if}
    </div>
  </form>
{/if}
