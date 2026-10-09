<script lang="ts">
  import { onMount } from "svelte";
  import { get, post, patch, put, del, errorText } from "../../lib/api";
  import { session } from "../../lib/session.svelte";
  import { mem } from "../../lib/format";
  import type { AdminUser, QuotaValues } from "../../lib/types";
  import QuotaFields from "../../components/QuotaFields.svelte";

  let users = $state<AdminUser[]>([]);
  let defaults = $state<QuotaValues | null>(null);
  let error = $state("");
  let notice = $state("");

  let newName = $state("");
  let newPass = $state("");
  let newRole = $state<"user" | "admin">("user");
  let editing = $state<string | null>(null);
  let editQuota = $state<QuotaValues | null>(null);
  let resetFor = $state<string | null>(null);
  let resetPass = $state("");

  async function load() {
    try {
      const [u, q] = await Promise.all([get<AdminUser[]>("/admin/users"), get<{ default: QuotaValues }>("/admin/quotas")]);
      users = u;
      defaults = q.default;
    } catch (e) {
      error = errorText(e);
    }
  }
  onMount(load);

  async function run(fn: () => Promise<unknown>, ok = "") {
    error = notice = "";
    try {
      await fn();
      notice = ok;
      await load();
      return true;
    } catch (e) {
      error = errorText(e);
      return false;
    }
  }

  async function create(e: SubmitEvent) {
    e.preventDefault();
    if (await run(() => post("/admin/users", { username: newName, password: newPass, role: newRole }), `User ${newName} created. They must change the password on first login.`)) {
      newName = newPass = "";
      newRole = "user";
    }
  }

  function limitText(u: AdminUser) {
    const l = u.limits;
    const parts = [`${u.usage.servers}/${l.max_servers ?? "∞"} servers`];
    if (l.max_total_memory_mb !== null) parts.push(`${mem(u.usage.running_memory_mb)}/${mem(l.max_total_memory_mb)} RAM`);
    return parts.join(" · ");
  }

  function genPassword() {
    const a = new Uint8Array(12);
    crypto.getRandomValues(a);
    return Array.from(a, (b) => "abcdefghjkmnpqrstuvwxyz23456789"[b % 31]).join("");
  }
</script>

<div class="space-y-6">
  {#if error}<p class="text-sm text-red-500">{error}</p>{/if}
  {#if notice}<p class="text-sm text-brand-500">{notice}</p>{/if}

  <form class="card grid gap-3 sm:grid-cols-[1fr_1fr_auto_auto] sm:items-end" onsubmit={create}>
    <div>
      <label class="label" for="nu">New user</label>
      <input id="nu" class="input" placeholder="username" bind:value={newName} required />
    </div>
    <div>
      <label class="label" for="np">Initial password</label>
      <div class="flex gap-1">
        <input id="np" class="input font-mono" minlength="10" bind:value={newPass} required />
        <button type="button" class="btn-secondary" title="Generate" onclick={() => (newPass = genPassword())}>⚄</button>
      </div>
    </div>
    <select class="input" bind:value={newRole} aria-label="Role">
      <option value="user">User</option>
      <option value="admin">Admin</option>
    </select>
    <button class="btn-primary">Create</button>
  </form>

  <div class="card overflow-x-auto p-0">
    <table class="w-full text-left text-sm">
      <thead class="border-b border-zinc-200 text-xs text-zinc-500 uppercase dark:border-zinc-800">
        <tr><th class="p-3">User</th><th class="p-3">Role</th><th class="p-3">Usage / limits</th><th class="p-3">Console</th><th class="p-3"></th></tr>
      </thead>
      <tbody class="divide-y divide-zinc-200 dark:divide-zinc-800">
        {#each users as u (u.id)}
          <tr class={u.disabled ? "opacity-50" : ""}>
            <td class="p-3">
              <span class="font-medium">{u.username}</span>
              {#if u.disabled}<span class="ml-1 text-xs text-red-500">disabled</span>{/if}
              {#if u.must_change_password}<span class="ml-1 text-xs text-amber-500">must change password</span>{/if}
            </td>
            <td class="p-3">
              <select
                class="input w-auto py-1"
                value={u.role}
                disabled={u.id === session.user?.id}
                onchange={(e) => run(() => patch(`/admin/users/${u.id}`, { role: e.currentTarget.value }))}
              >
                <option value="user">user</option>
                <option value="admin">admin</option>
              </select>
            </td>
            <td class="p-3 text-zinc-500">{limitText(u)}</td>
            <td class="p-3">
              <input
                type="checkbox"
                checked={u.console_enabled}
                aria-label="Console enabled"
                onchange={(e) => run(() => patch(`/admin/users/${u.id}`, { console_enabled: e.currentTarget.checked }))}
              />
            </td>
            <td class="p-3">
              <div class="flex flex-wrap justify-end gap-1">
                <button class="btn-secondary" onclick={() => ((editing = editing === u.id ? null : u.id), (editQuota = { ...u.quota }))}>Quota</button>
                <button class="btn-secondary" onclick={() => ((resetFor = resetFor === u.id ? null : u.id), (resetPass = genPassword()))}>Password</button>
                {#if u.id !== session.user?.id}
                  <button class="btn-secondary" onclick={() => run(() => patch(`/admin/users/${u.id}`, { disabled: !u.disabled }))}>
                    {u.disabled ? "Enable" : "Disable"}
                  </button>
                  <button
                    class="btn-secondary text-red-500"
                    onclick={() => confirm(`Delete user ${u.username}?`) && run(() => del(`/admin/users/${u.id}`), `User ${u.username} deleted.`)}
                    >Delete</button
                  >
                {/if}
              </div>
            </td>
          </tr>
          {#if editing === u.id && editQuota}
            <tr>
              <td colspan="5" class="bg-zinc-50 p-3 dark:bg-zinc-950">
                <p class="muted mb-2">Empty fields use the default quota.</p>
                <QuotaFields bind:value={editQuota} emptyLabel="default" />
                <div class="mt-3 flex gap-2">
                  <button class="btn-primary" onclick={() => run(() => put(`/admin/quotas/${u.id}`, editQuota), "Quota saved.").then((ok) => ok && (editing = null))}>Save quota</button>
                  <button class="btn-secondary" onclick={() => (editing = null)}>Cancel</button>
                </div>
              </td>
            </tr>
          {/if}
          {#if resetFor === u.id}
            <tr>
              <td colspan="5" class="bg-zinc-50 p-3 dark:bg-zinc-950">
                <div class="flex flex-wrap items-end gap-2">
                  <div>
                    <label class="label" for="rp">New password for {u.username}</label>
                    <input id="rp" class="input font-mono" minlength="10" bind:value={resetPass} />
                  </div>
                  <button
                    class="btn-primary"
                    onclick={() =>
                      run(() => post(`/admin/users/${u.id}/password`, { password: resetPass }), `Password reset. ${u.username} must change it on next login.`).then(
                        (ok) => ok && (resetFor = null),
                      )}>Reset password</button
                  >
                  <button class="btn-secondary" onclick={() => (resetFor = null)}>Cancel</button>
                </div>
              </td>
            </tr>
          {/if}
        {/each}
      </tbody>
    </table>
  </div>

  {#if defaults}
    <div class="card space-y-3">
      <h2 class="font-medium">Default quota</h2>
      <p class="muted">Applies to every user without an override. Empty fields mean unlimited.</p>
      <QuotaFields bind:value={defaults} emptyLabel="unlimited" />
      <button class="btn-primary" onclick={() => run(() => put("/admin/quotas/default", defaults), "Default quota saved.")}>Save default</button>
    </div>
  {/if}
</div>
