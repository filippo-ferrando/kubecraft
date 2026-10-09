<script lang="ts">
  import { onMount } from "svelte";
  import { get, post, errorText } from "../lib/api";
  import { navigate, link } from "../lib/router.svelte";
  import { contentKind, planBlocked, planSummary, typeGroups, typeHints, typeLabels } from "../lib/format";
  import type { Meta, Plan, Quota, Server, Settings, VersionList } from "../lib/types";
  import SettingsForm from "../components/SettingsForm.svelte";
  import ContentPicker from "../components/ContentPicker.svelte";

  let step = $state(0);

  let meta = $state<Meta | null>(null);
  let plans = $state<Plan[]>([]);
  let quota = $state<Quota | null>(null);
  let versions = $state<VersionList | null>(null);
  let loadError = $state("");

  let displayName = $state("");
  let slug = $state("");
  let slugTouched = $state(false);
  let slugStatus = $state<{ checking: boolean; ok: boolean; reason: string }>({ checking: false, ok: false, reason: "" });
  let type = $state("PAPER");
  let useLatest = $state(true);
  let version = $state("");
  let planId = $state("");
  let settings = $state<Settings | null>(null);
  let projects = $state<string[]>([]);
  let eula = $state(false);
  let creating = $state(false);
  let error = $state("");

  onMount(async () => {
    try {
      [meta, plans, quota] = await Promise.all([get<Meta>("/meta"), get<Plan[]>("/plans"), get<Quota>("/me/quota")]);
      settings = $state.snapshot(meta.default_settings);
      const first = plans.find((p) => !planBlocked(p, quota!.limits, quota!.usage));
      planId = first?.id ?? "";
    } catch (e) {
      loadError = errorText(e);
    }
    get<VersionList>("/versions")
      .then((v) => (versions = v))
      .catch(() => (versions = { latest: "", versions: [] }));
  });

  function toSlug(s: string) {
    return s
      .toLowerCase()
      .normalize("NFKD")
      .replace(/[^a-z0-9]+/g, "-")
      .replace(/^-+|-+$/g, "")
      .slice(0, 32)
      .replace(/-+$/, "");
  }

  $effect(() => {
    if (!slugTouched) slug = toSlug(displayName);
  });

  // Debounced availability check.
  let checkTimer: ReturnType<typeof setTimeout>;
  $effect(() => {
    const s = slug;
    clearTimeout(checkTimer);
    if (!s) {
      slugStatus = { checking: false, ok: false, reason: "" };
      return;
    }
    slugStatus = { checking: true, ok: false, reason: "" };
    checkTimer = setTimeout(async () => {
      try {
        const r = await get<{ available: boolean; reason?: string }>("/servers/slug-available?slug=" + encodeURIComponent(s));
        if (s === slug) slugStatus = { checking: false, ok: r.available, reason: r.reason ?? "" };
      } catch (e) {
        slugStatus = { checking: false, ok: false, reason: errorText(e) };
      }
    }, 300);
  });

  // The add-ons step only exists for types that load mods or plugins.
  const kind = $derived(contentKind(type));
  const steps = $derived([
    "name",
    "type",
    "version",
    ...(kind ? ["content"] : []),
    "plan",
    "settings",
    "create",
  ]);
  const stepLabel: Record<string, string> = { name: "Name", type: "Type", version: "Version", plan: "Plan", settings: "Settings", create: "Create" };
  const label = (k: string) => (k === "content" ? (kind === "plugin" ? "Plugins" : "Mods") : stepLabel[k]);
  const key = $derived(steps[step]);

  const versionOk = $derived(useLatest || /^(\d{1,2}\.\d{1,2}(\.\d{1,2})?(-(pre|rc)\d{1,2})?|\d{2}w\d{2}[a-z])$/.test(version));
  const plan = $derived(plans.find((p) => p.id === planId));
  const canNext = $derived(
    ({
      name: slugStatus.ok && displayName.trim().length > 0,
      type: !!type,
      version: versionOk,
      content: true,
      plan: !!plan && !!quota && !planBlocked(plan, quota.limits, quota.usage),
      settings: !!settings,
      create: eula,
    })[key] ?? false,
  );

  async function create() {
    creating = true;
    error = "";
    try {
      const s = await post<Server>("/servers", {
        slug,
        display_name: displayName.trim(),
        type,
        version: useLatest ? "LATEST" : version,
        plan_id: planId,
        settings: { ...settings, modrinth_projects: kind ? projects : [] },
        accept_eula: eula,
      });
      navigate(`/servers/${s.id}`, true);
    } catch (e) {
      error = errorText(e);
    } finally {
      creating = false;
    }
  }
</script>

<div class="mx-auto max-w-2xl space-y-6">
  <div class="flex items-center gap-3">
    <a href="/" use:link class="text-zinc-500 hover:text-zinc-900 dark:hover:text-zinc-100">←</a>
    <h1 class="text-2xl font-semibold">New server</h1>
  </div>

  <ol class="flex gap-1 overflow-x-auto text-xs">
    {#each steps as s, i}
      <li class="flex-1">
        <button
          class="w-full border-t-2 pt-2 text-left {i <= step ? 'border-brand-500 text-zinc-900 dark:text-zinc-100' : 'border-zinc-300 text-zinc-500 dark:border-zinc-700'}"
          disabled={i > step}
          onclick={() => (step = i)}>{i + 1}. {label(s)}</button
        >
      </li>
    {/each}
  </ol>

  {#if loadError}
    <p class="text-red-500">{loadError}</p>
  {:else if !meta || !quota}
    <p class="muted">Loading...</p>
  {:else}
    <div class="card space-y-4">
      {#if key === "name"}
        <div>
          <label class="label" for="dn">Display name</label>
          <input id="dn" class="input" maxlength="64" bind:value={displayName} placeholder="Survival with friends" />
        </div>
        <div>
          <label class="label" for="slug">Address</label>
          <div class="flex items-center gap-1">
            <input
              id="slug"
              class="input font-mono"
              maxlength="32"
              bind:value={slug}
              oninput={() => (slugTouched = true)}
              placeholder="survival"
            />
            <span class="shrink-0 font-mono text-sm text-zinc-500">.{meta.server_base_domain}</span>
          </div>
          <p class="mt-1 text-sm {slugStatus.ok ? 'text-brand-500' : 'text-red-500'}">
            {#if slugStatus.checking}<span class="text-zinc-500">Checking...</span>
            {:else if slugStatus.ok}Available
            {:else}{slugStatus.reason}{/if}
          </p>
        </div>
      {:else if key === "type"}
        {#each typeGroups as g (g.label)}
          <div>
            <h3 class="text-sm font-medium">{g.label} <span class="font-normal text-zinc-500">· {g.hint}</span></h3>
            <div class="mt-2 grid gap-3 sm:grid-cols-3">
              {#each g.types.filter((t) => meta!.types.includes(t)) as t}
                <button
                  class="rounded-lg border p-3 text-left transition {type === t
                    ? 'border-brand-500 bg-brand-500/10'
                    : 'border-zinc-200 hover:border-zinc-400 dark:border-zinc-700'}"
                  onclick={() => {
                    if (contentKind(t) !== kind) projects = [];
                    type = t;
                  }}
                >
                  <div class="font-medium">{typeLabels[t] ?? t}</div>
                  <div class="muted">{typeHints[t] ?? ""}</div>
                </button>
              {/each}
            </div>
          </div>
        {/each}
      {:else if key === "version"}
        <label class="flex items-center gap-2">
          <input type="checkbox" bind:checked={useLatest} />
          <span>Latest release {versions?.latest ? `(${versions.latest})` : ""}</span>
        </label>
        {#if !useLatest}
          <div>
            <label class="label" for="ver">Version</label>
            <input id="ver" class="input font-mono" list="versions" bind:value={version} placeholder="1.21.4" />
            <datalist id="versions">
              {#each versions?.versions ?? [] as v}<option value={v.id}></option>{/each}
            </datalist>
            {#if version && !versionOk}<p class="mt-1 text-sm text-red-500">Not a valid Minecraft version.</p>{/if}
            {#if type !== "VANILLA"}
              <p class="muted mt-1">Check that {typeLabels[type]} supports this version.</p>
            {/if}
          </div>
        {/if}
      {:else if key === "content"}
        <ContentPicker {type} version={useLatest ? "LATEST" : version} bind:selected={projects} />
      {:else if key === "plan"}
        <div class="space-y-2">
          {#each plans as p (p.id)}
            {@const blocked = planBlocked(p, quota.limits, quota.usage)}
            <button
              disabled={!!blocked}
              class="flex w-full items-center justify-between rounded-lg border p-3 text-left transition disabled:opacity-50 {planId === p.id
                ? 'border-brand-500 bg-brand-500/10'
                : 'border-zinc-200 hover:border-zinc-400 dark:border-zinc-700'}"
              onclick={() => (planId = p.id)}
            >
              <div>
                <div class="font-medium">{p.name}</div>
                <div class="muted">{planSummary(p)}</div>
              </div>
              {#if blocked}<span class="text-xs text-red-500">{blocked}</span>{/if}
            </button>
          {/each}
          {#if plans.length === 0}<p class="muted">No plans available. Ask an admin to create one.</p>{/if}
        </div>
      {:else if key === "settings" && settings}
        <SettingsForm bind:settings {meta} creating />
      {:else if key === "create"}
        <dl class="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1 text-sm">
          <dt class="text-zinc-500">Name</dt>
          <dd>{displayName}</dd>
          <dt class="text-zinc-500">Address</dt>
          <dd class="font-mono">{slug}.{meta.server_base_domain}</dd>
          <dt class="text-zinc-500">Type</dt>
          <dd>{typeLabels[type]} {useLatest ? "latest" : version}</dd>
          <dt class="text-zinc-500">Plan</dt>
          <dd>{plan?.name} ({plan && planSummary(plan)})</dd>
          {#if kind}
            <dt class="text-zinc-500">{kind === "plugin" ? "Plugins" : "Mods"}</dt>
            <dd>{projects.length ? projects.join(", ") : "none"}</dd>
          {/if}
        </dl>
        <label class="flex items-start gap-2 text-sm">
          <input type="checkbox" class="mt-1" bind:checked={eula} />
          <span>
            I accept the <a class="text-brand-500 underline" href="https://aka.ms/MinecraftEULA" target="_blank" rel="noreferrer"
              >Minecraft End User License Agreement</a
            >.
          </span>
        </label>
        {#if error}<p class="text-sm text-red-500">{error}</p>{/if}
      {/if}
    </div>

    <div class="flex justify-between">
      <button class="btn-secondary" disabled={step === 0} onclick={() => step--}>Back</button>
      {#if step < steps.length - 1}
        <button class="btn-primary" disabled={!canNext} onclick={() => step++}>Next</button>
      {:else}
        <button class="btn-primary" disabled={!canNext || creating} onclick={create}>{creating ? "Creating..." : "Create server"}</button>
      {/if}
    </div>
  {/if}
</div>
