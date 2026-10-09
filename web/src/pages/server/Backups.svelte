<script lang="ts">
  import { onMount } from "svelte";
  import { get } from "../../lib/api";
  import type { Meta, Server } from "../../lib/types";

  let { server }: { server: Server; onchange: (s: Server) => void } = $props();
  let meta = $state<Meta | null>(null);
  onMount(async () => (meta = await get<Meta>("/meta")));

  const down = $derived(server.status.state !== "running");
</script>

<div class="card space-y-4">
  <h2 class="font-medium">Download a backup</h2>
  {#if meta && !meta.files}
    <p class="muted">Backups are disabled on this installation.</p>
  {:else}
    <p class="text-sm">
      The download is built on the fly from the server volume. On a running server, autosave is paused and the world is saved to disk
      first, so the archive is consistent.
    </p>
    <div class="flex flex-wrap gap-3">
      <a class="btn-primary" href={`/api/servers/${server.id}/backup?scope=world`} download>Download world</a>
      <a class="btn-secondary" href={`/api/servers/${server.id}/backup?scope=full`} download>Download full server</a>
    </div>
    <ul class="muted list-disc space-y-1 pl-5">
      <li><b>World</b>: world folders (overworld, nether, end), player lists and server.properties.</li>
      <li><b>Full server</b>: everything including plugins, mods and their configs, without the server jar and caches.</li>
      {#if down}<li>The server is not running: a temporary pod mounts the volume, the download can take up to a minute to start.</li>{/if}
    </ul>
  {/if}
</div>
