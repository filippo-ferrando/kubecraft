<script lang="ts">
  import { post, errorText } from "../lib/api";
  import { session, refreshSession } from "../lib/session.svelte";
  import { navigate } from "../lib/router.svelte";

  let current = $state("");
  let next = $state("");
  let confirm = $state("");
  let error = $state("");
  let done = $state(false);
  let busy = $state(false);

  const forced = $derived(session.user?.must_change_password ?? false);

  async function submit(e: SubmitEvent) {
    e.preventDefault();
    error = "";
    done = false;
    if (next !== confirm) {
      error = "The new passwords do not match.";
      return;
    }
    busy = true;
    try {
      await post("/me/password", { current_password: current, new_password: next });
      const wasForced = forced;
      await refreshSession();
      current = next = confirm = "";
      done = true;
      if (wasForced) navigate("/", true);
    } catch (err) {
      error = errorText(err);
    } finally {
      busy = false;
    }
  }
</script>

<div class="mx-auto max-w-md space-y-4">
  <h1 class="text-2xl font-semibold">Account</h1>
  {#if forced}
    <p class="rounded-lg border border-amber-500/40 bg-amber-500/10 p-3 text-sm">
      A new password is required before continuing.
    </p>
  {/if}
  <form class="card space-y-4" onsubmit={submit}>
    <h2 class="font-medium">Change password</h2>
    <div>
      <label class="label" for="cur">Current password</label>
      <input id="cur" class="input" type="password" autocomplete="current-password" bind:value={current} required />
    </div>
    <div>
      <label class="label" for="new">New password</label>
      <input id="new" class="input" type="password" autocomplete="new-password" minlength="10" bind:value={next} required />
      <p class="muted mt-1">At least 10 characters.</p>
    </div>
    <div>
      <label class="label" for="confirm">Confirm new password</label>
      <input id="confirm" class="input" type="password" autocomplete="new-password" bind:value={confirm} required />
    </div>
    {#if error}<p class="text-sm text-red-500">{error}</p>{/if}
    {#if done}<p class="text-sm text-brand-500">Password changed.</p>{/if}
    <button class="btn-primary" disabled={busy}>Change password</button>
  </form>
</div>
