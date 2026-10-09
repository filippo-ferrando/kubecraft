<script lang="ts">
  import { onMount } from "svelte";
  import { get, post, patch, del, errorText } from "../../lib/api";
  import { planSummary } from "../../lib/format";
  import type { Plan } from "../../lib/types";

  let plans = $state<Plan[]>([]);
  let error = $state("");
  const blank = (): Omit<Plan, "id"> => ({ name: "", cpu_request_m: 500, cpu_limit_m: 1000, memory_limit_mb: 2048, storage_gb: 5, active: true });
  let form = $state<Omit<Plan, "id">>(blank());
  let editId = $state<string | null>(null);

  async function load() {
    try {
      plans = await get<Plan[]>("/admin/plans");
    } catch (e) {
      error = errorText(e);
    }
  }
  onMount(load);

  async function run(fn: () => Promise<unknown>) {
    error = "";
    try {
      await fn();
      await load();
      return true;
    } catch (e) {
      error = errorText(e);
      return false;
    }
  }

  async function save(e: SubmitEvent) {
    e.preventDefault();
    const ok = await run(() => (editId ? patch(`/admin/plans/${editId}`, form) : post("/admin/plans", form)));
    if (ok) {
      form = blank();
      editId = null;
    }
  }

  function edit(p: Plan) {
    editId = p.id;
    const { id: _, ...rest } = p;
    form = rest;
  }
</script>

<div class="space-y-6">
  {#if error}<p class="text-sm text-red-500">{error}</p>{/if}
  <div class="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
    {#each plans as p (p.id)}
      <div class="card space-y-2 {p.active ? '' : 'opacity-60'}">
        <div class="flex items-center justify-between">
          <h3 class="font-medium">{p.name}</h3>
          <span class="text-xs {p.active ? 'text-brand-500' : 'text-zinc-500'}">{p.active ? "active" : "inactive"}</span>
        </div>
        <p class="text-sm">{planSummary(p)}</p>
        <p class="muted">CPU request {p.cpu_request_m}m</p>
        <div class="flex gap-2">
          <button class="btn-secondary" onclick={() => edit(p)}>Edit</button>
          <button class="btn-secondary" onclick={() => run(() => patch(`/admin/plans/${p.id}`, { active: !p.active }))}>
            {p.active ? "Deactivate" : "Activate"}
          </button>
          <button class="btn-secondary text-red-500" onclick={() => confirm(`Delete plan ${p.name}?`) && run(() => del(`/admin/plans/${p.id}`))}>Delete</button>
        </div>
      </div>
    {/each}
  </div>

  <form class="card space-y-4" onsubmit={save}>
    <h2 class="font-medium">{editId ? "Edit plan" : "New plan"}</h2>
    <div class="grid gap-3 sm:grid-cols-3">
      <div>
        <label class="label" for="pn">Name</label>
        <input id="pn" class="input" maxlength="32" bind:value={form.name} required />
      </div>
      <div>
        <label class="label" for="pcr">CPU request (millicores)</label>
        <input id="pcr" class="input" type="number" min="100" bind:value={form.cpu_request_m} />
      </div>
      <div>
        <label class="label" for="pcl">CPU limit (millicores)</label>
        <input id="pcl" class="input" type="number" min="100" bind:value={form.cpu_limit_m} />
      </div>
      <div>
        <label class="label" for="pm">Memory (MB)</label>
        <input id="pm" class="input" type="number" min="1024" step="256" bind:value={form.memory_limit_mb} />
      </div>
      <div>
        <label class="label" for="ps">Disk (GB)</label>
        <input id="ps" class="input" type="number" min="1" bind:value={form.storage_gb} />
      </div>
      <label class="flex items-center gap-2 self-end pb-2 text-sm"><input type="checkbox" bind:checked={form.active} /> Active</label>
    </div>
    <p class="muted">Servers on a changed plan get the new limits on their next restart. Disk can only grow.</p>
    <div class="flex gap-2">
      <button class="btn-primary">{editId ? "Save plan" : "Create plan"}</button>
      {#if editId}<button type="button" class="btn-secondary" onclick={() => ((editId = null), (form = blank()))}>Cancel</button>{/if}
    </div>
  </form>
</div>
