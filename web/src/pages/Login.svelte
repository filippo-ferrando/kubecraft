<script lang="ts">
  import { login, session } from "../lib/session.svelte";
  import { navigate, route } from "../lib/router.svelte";
  import { errorText } from "../lib/api";

  let username = $state("");
  let password = $state("");
  let error = $state("");
  let busy = $state(false);

  async function submit(e: SubmitEvent) {
    e.preventDefault();
    busy = true;
    error = "";
    try {
      await login(username, password);
      const next = route.query.get("next");
      navigate(session.user?.must_change_password ? "/account" : next && next.startsWith("/") ? next : "/", true);
    } catch (err) {
      error = errorText(err);
    } finally {
      busy = false;
    }
  }
</script>

<div class="flex min-h-screen items-center justify-center p-4">
  <form class="card w-full max-w-sm space-y-4 p-6" onsubmit={submit}>
    <div class="flex items-center gap-2">
      <img src="/favicon.svg" alt="" class="h-8 w-8" />
      <h1 class="text-xl font-semibold">kubecraft</h1>
    </div>
    <div>
      <label class="label" for="username">Username</label>
      <input id="username" class="input" autocomplete="username" bind:value={username} required />
    </div>
    <div>
      <label class="label" for="password">Password</label>
      <input id="password" class="input" type="password" autocomplete="current-password" bind:value={password} required />
    </div>
    {#if error}<p class="text-sm text-red-500">{error}</p>{/if}
    <button class="btn-primary w-full" disabled={busy}>{busy ? "Logging in..." : "Log in"}</button>
  </form>
</div>
