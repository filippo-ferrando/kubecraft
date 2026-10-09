<script lang="ts">
  import { onMount } from "svelte";
  import { get, put, errorText } from "../../lib/api";
  import { bytes } from "../../lib/format";
  import type { FileEntry, Server } from "../../lib/types";

  let { server }: { server: Server; onchange: (s: Server) => void } = $props();

  let dir = $state(".");
  let entries = $state<FileEntry[]>([]);
  let loading = $state(false);
  let error = $state("");
  let open = $state<{ path: string; content: string; original: string } | null>(null);
  let saving = $state(false);
  let notice = $state("");

  async function load(path: string) {
    loading = true;
    error = "";
    try {
      const r = await get<{ path: string; entries: FileEntry[] }>(`/servers/${server.id}/files?path=${encodeURIComponent(path)}`);
      dir = r.path;
      entries = r.entries;
    } catch (e) {
      error = errorText(e);
    } finally {
      loading = false;
    }
  }
  onMount(() => load("."));

  async function edit(e: FileEntry) {
    error = notice = "";
    try {
      const r = await get<{ content: string }>(`/servers/${server.id}/files/content?path=${encodeURIComponent(e.path)}`);
      open = { path: e.path, content: r.content, original: r.content };
    } catch (err) {
      error = errorText(err);
    }
  }

  async function save() {
    if (!open) return;
    saving = true;
    error = notice = "";
    try {
      await put(`/servers/${server.id}/files/content`, { path: open.path, content: open.content });
      open.original = open.content;
      notice = "Saved. Restart the server (or reload the plugin) to apply.";
    } catch (e) {
      error = errorText(e);
    } finally {
      saving = false;
    }
  }

  function close() {
    if (open && open.content !== open.original && !confirm("Discard unsaved changes?")) return;
    open = null;
  }

  const crumbs = $derived(dir === "." ? [] : dir.split("/"));
</script>

<div class="space-y-4">
  {#if error}<p class="text-sm text-red-500">{error}</p>{/if}
  {#if notice}<p class="text-sm text-brand-500">{notice}</p>{/if}

  {#if open}
    <div class="card space-y-3">
      <div class="flex flex-wrap items-center justify-between gap-2">
        <span class="font-mono text-sm break-all">{open.path}</span>
        <div class="flex gap-2">
          <button class="btn-primary" disabled={saving || open.content === open.original} onclick={save}>{saving ? "Saving..." : "Save"}</button>
          <button class="btn-secondary" onclick={close}>Close</button>
        </div>
      </div>
      <textarea
        class="input h-[60vh] font-mono text-xs leading-relaxed"
        spellcheck="false"
        bind:value={open.content}
        onkeydown={(e) => {
          if ((e.ctrlKey || e.metaKey) && e.key === "s") {
            e.preventDefault();
            save();
          }
        }}
      ></textarea>
      <p class="muted">Text config files up to 1 MB. Settings managed by kubecraft (like the MOTD in server.properties) are reset on start.</p>
    </div>
  {:else}
    <div class="card space-y-3 p-0">
      <div class="flex flex-wrap items-center gap-1 border-b border-zinc-200 p-3 font-mono text-sm dark:border-zinc-800">
        <button class="text-brand-500 hover:underline" onclick={() => load(".")}>/data</button>
        {#each crumbs as c, i}
          <span class="text-zinc-500">/</span>
          <button class="text-brand-500 hover:underline" onclick={() => load(crumbs.slice(0, i + 1).join("/"))}>{c}</button>
        {/each}
        {#if loading}<span class="ml-2 text-zinc-500">loading...</span>{/if}
      </div>
      <ul class="divide-y divide-zinc-200 dark:divide-zinc-800">
        {#if dir !== "."}
          <li>
            <button class="w-full px-3 py-2 text-left text-sm hover:bg-zinc-100 dark:hover:bg-zinc-800" onclick={() => load(crumbs.slice(0, -1).join("/") || ".")}
              >..</button
            >
          </li>
        {/if}
        {#each entries as e (e.path)}
          <li class="flex items-center gap-3 px-3 py-2 text-sm">
            <span class="w-5 text-center">{e.dir ? "📁" : e.editable ? "📝" : "📄"}</span>
            {#if e.dir}
              <button class="flex-1 text-left font-mono hover:underline" onclick={() => load(e.path)}>{e.name}</button>
            {:else if e.editable}
              <button class="flex-1 text-left font-mono hover:underline" onclick={() => edit(e)}>{e.name}</button>
            {:else}
              <span class="flex-1 font-mono text-zinc-500">{e.name}</span>
            {/if}
            {#if !e.dir}<span class="text-xs text-zinc-500">{bytes(e.size)}</span>{/if}
          </li>
        {:else}
          {#if !loading}<li class="muted px-3 py-3">Empty folder.</li>{/if}
        {/each}
      </ul>
    </div>
    <p class="muted">Config files of plugins and mods are usually in plugins/ or config/. They appear after the first start.</p>
  {/if}
</div>
