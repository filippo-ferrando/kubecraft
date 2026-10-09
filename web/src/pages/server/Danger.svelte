<script lang="ts">
  import { del, errorText } from "../../lib/api";
  import { navigate } from "../../lib/router.svelte";
  import type { Server } from "../../lib/types";

  let { server }: { server: Server; onchange: (s: Server) => void } = $props();
  let confirm = $state("");
  let keepData = $state(false);
  let busy = $state(false);
  let error = $state("");

  async function remove() {
    busy = true;
    error = "";
    try {
      await del(`/servers/${server.id}`, { confirm, keep_data: keepData });
      navigate("/", true);
    } catch (e) {
      error = errorText(e);
    } finally {
      busy = false;
    }
  }
</script>

<div class="card space-y-4 border-red-500/40">
  <h2 class="font-medium text-red-500">Delete server</h2>
  <p class="text-sm">This removes the server and, unless kept, its world. This cannot be undone.</p>
  <label class="flex items-center gap-2 text-sm">
    <input type="checkbox" bind:checked={keepData} />
    Keep the world volume (an admin can recover it later; the name stays reserved)
  </label>
  <div>
    <label class="label" for="confirm">Type <span class="font-mono">{server.slug}</span> to confirm</label>
    <input id="confirm" class="input font-mono" bind:value={confirm} autocomplete="off" />
  </div>
  {#if error}<p class="text-sm text-red-500">{error}</p>{/if}
  <button class="btn-danger" disabled={confirm !== server.slug || busy} onclick={remove}>
    {busy ? "Deleting..." : keepData ? "Delete server, keep world" : "Delete server and world"}
  </button>
</div>
