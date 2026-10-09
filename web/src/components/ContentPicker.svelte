<script lang="ts">
  import { onMount } from "svelte";
  import { get, errorText } from "../lib/api";
  import { contentKind, count } from "../lib/format";
  import type { CatalogProject } from "../lib/types";

  // selected holds Modrinth slugs; the server downloads them on start.
  let { type, version, selected = $bindable() }: { type: string; version: string; selected: string[] } = $props();

  const kind = $derived(contentKind(type));
  const noun = $derived(kind === "plugin" ? "plugins" : "mods");

  let query = $state("");
  let hits = $state<CatalogProject[]>([]);
  let total = $state(0);
  let loading = $state(false);
  let error = $state("");
  let gameVersion = $state("");
  // Details of selected projects, for names and icons.
  let known = $state<Record<string, CatalogProject>>({});

  async function search(offset = 0) {
    loading = true;
    error = "";
    try {
      const params = new URLSearchParams({ type, version, q: query.trim(), offset: String(offset) });
      const r = await get<{ game_version: string; result: { hits: CatalogProject[]; total_hits: number } }>(`/catalog/search?${params}`);
      gameVersion = r.game_version;
      hits = offset ? [...hits, ...r.result.hits] : r.result.hits;
      total = r.result.total_hits;
      for (const h of r.result.hits) known[h.slug] = h;
    } catch (e) {
      error = errorText(e);
    } finally {
      loading = false;
    }
  }

  let timer: ReturnType<typeof setTimeout>;
  function onInput() {
    clearTimeout(timer);
    timer = setTimeout(() => search(), 300);
  }

  onMount(async () => {
    search();
    const missing = selected.filter((s) => !known[s]);
    if (missing.length) {
      try {
        for (const p of await get<CatalogProject[]>(`/catalog/projects?ids=${encodeURIComponent(missing.join(","))}`)) {
          known[p.slug] = p;
          known[p.id] = p;
        }
      } catch {
        // Names fall back to slugs.
      }
    }
  });

  const isSelected = (p: CatalogProject) => selected.includes(p.slug) || selected.includes(p.id);
  function toggle(p: CatalogProject) {
    selected = isSelected(p) ? selected.filter((s) => s !== p.slug && s !== p.id) : [...selected, p.slug];
  }
</script>

{#if !kind}
  <p class="muted">Vanilla servers do not support mods or plugins. Pick Paper or Purpur for plugins, Fabric, Forge or NeoForge for mods.</p>
{:else}
  <div class="space-y-4">
    <div>
      <h3 class="mb-2 text-sm font-medium">Selected {noun} ({selected.length})</h3>
      {#if selected.length === 0}
        <p class="muted">None yet. Search the Modrinth catalog below.</p>
      {:else}
        <div class="flex flex-wrap gap-2">
          {#each selected as slug (slug)}
            {@const p = known[slug]}
            <span class="inline-flex items-center gap-1.5 rounded-full border border-zinc-300 py-0.5 pr-1 pl-2 text-sm dark:border-zinc-700">
              {#if p?.icon_url}<img src={p.icon_url} alt="" class="h-4 w-4 rounded" />{/if}
              {p?.title ?? slug}
              <button
                type="button"
                class="rounded-full px-1 text-zinc-500 hover:text-red-500"
                aria-label="Remove"
                onclick={() => (selected = selected.filter((s) => s !== slug))}>×</button
              >
            </span>
          {/each}
        </div>
      {/if}
    </div>

    <div>
      <input class="input" placeholder={`Search ${noun} on Modrinth`} bind:value={query} oninput={onInput} />
      <p class="muted mt-1">
        Showing server-side {noun} for {type.charAt(0) + type.slice(1).toLowerCase()}{gameVersion ? ` ${gameVersion}` : ""}. Required dependencies are installed
        automatically.
      </p>
    </div>
    {#if error}<p class="text-sm text-red-500">{error}</p>{/if}

    <ul class="divide-y divide-zinc-200 dark:divide-zinc-800">
      {#each hits as p (p.id)}
        <li class="flex items-center gap-3 py-2">
          {#if p.icon_url}
            <img src={p.icon_url} alt="" class="h-10 w-10 shrink-0 rounded-lg" loading="lazy" />
          {:else}
            <div class="h-10 w-10 shrink-0 rounded-lg bg-zinc-200 dark:bg-zinc-800"></div>
          {/if}
          <div class="min-w-0 flex-1">
            <a href={`https://modrinth.com/project/${p.slug}`} target="_blank" rel="noreferrer" class="font-medium hover:underline">{p.title}</a>
            <span class="text-xs text-zinc-500">· {count(p.downloads)} downloads</span>
            <p class="truncate text-sm text-zinc-500">{p.description}</p>
          </div>
          <button type="button" class={isSelected(p) ? "btn-secondary" : "btn-primary"} onclick={() => toggle(p)}>
            {isSelected(p) ? "Remove" : "Add"}
          </button>
        </li>
      {:else}
        {#if !loading}<li class="muted py-3">No results.</li>{/if}
      {/each}
    </ul>
    {#if loading}<p class="muted">Searching...</p>{/if}
    {#if !loading && hits.length < total}
      <button type="button" class="btn-secondary" onclick={() => search(hits.length)}>Load more</button>
    {/if}
    <p class="text-xs text-zinc-500">Catalog by Modrinth. Files are downloaded by the server on start, nothing is stored here.</p>
  </div>
{/if}
