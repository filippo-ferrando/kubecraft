<script lang="ts">
  import { untrack } from "svelte";
  import { patch, errorText } from "../../lib/api";
  import { contentKind } from "../../lib/format";
  import type { Server } from "../../lib/types";
  import ContentPicker from "../../components/ContentPicker.svelte";

  let { server, onchange }: { server: Server; onchange: (s: Server) => void } = $props();

  // Copied once so polling does not overwrite edits in progress.
  let selected = $state<string[]>(untrack(() => [...server.settings.modrinth_projects]));
  let saving = $state(false);
  let error = $state("");
  let saved = $state(false);

  const kind = $derived(contentKind(server.type));
  const dirty = $derived(selected.join(",") !== server.settings.modrinth_projects.join(","));

  async function save() {
    saving = true;
    error = "";
    saved = false;
    try {
      const updated = await patch<Server>(`/servers/${server.id}`, { settings: { ...server.settings, modrinth_projects: selected } });
      onchange(updated);
      selected = [...updated.settings.modrinth_projects];
      saved = true;
    } catch (e) {
      error = errorText(e);
    } finally {
      saving = false;
    }
  }
</script>

<div class="card space-y-4">
  <ContentPicker type={server.type} version={server.version} bind:selected />
  {#if kind}
    <div class="flex items-center gap-3 border-t border-zinc-200 pt-4 dark:border-zinc-800">
      <button class="btn-primary" disabled={!dirty || saving} onclick={save}>{saving ? "Saving..." : "Save"}</button>
      {#if saved}<span class="text-sm text-brand-500">Saved. Restart the server to install the changes.</span>{/if}
      {#if error}<span class="text-sm text-red-500">{error}</span>{/if}
    </div>
  {/if}
</div>
