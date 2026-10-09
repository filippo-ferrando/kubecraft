<script lang="ts">
  import type { Meta, Settings } from "../lib/types";

  // creating enables world generation fields, which only apply to new worlds.
  let { settings = $bindable(), meta, creating = false }: { settings: Settings; meta: Meta; creating?: boolean } = $props();

  const cap = (s: string) => s.charAt(0).toUpperCase() + s.slice(1).replace("_", " ");
</script>

<div class="grid gap-4 sm:grid-cols-2">
  <div class="sm:col-span-2">
    <label class="label" for="motd">Message of the day</label>
    <input id="motd" class="input" maxlength="120" bind:value={settings.motd} />
  </div>
  <div>
    <label class="label" for="diff">Difficulty</label>
    <select id="diff" class="input" bind:value={settings.difficulty}>
      {#each meta.difficulties as d}<option value={d}>{cap(d)}</option>{/each}
    </select>
  </div>
  <div>
    <label class="label" for="gm">Game mode</label>
    <select id="gm" class="input" bind:value={settings.gamemode}>
      {#each meta.gamemodes as g}<option value={g}>{cap(g)}</option>{/each}
    </select>
  </div>
  <div>
    <label class="label" for="mp">Max players</label>
    <input id="mp" class="input" type="number" min="1" max="200" bind:value={settings.max_players} />
  </div>
  <div>
    <label class="label" for="vd">View distance (chunks)</label>
    <input id="vd" class="input" type="number" min="2" max="32" bind:value={settings.view_distance} />
  </div>
  <div>
    <label class="label" for="lt">World type</label>
    <select id="lt" class="input" bind:value={settings.level_type} disabled={!creating}>
      {#each meta.level_types as l}<option value={l}>{cap(l)}</option>{/each}
    </select>
  </div>
  <div>
    <label class="label" for="seed">Seed</label>
    <input id="seed" class="input font-mono" maxlength="64" bind:value={settings.seed} disabled={!creating} placeholder="random" />
  </div>
  {#if !creating}
    <p class="muted sm:col-span-2">World type and seed only apply when a world is generated.</p>
  {/if}
  <div class="flex flex-col gap-2 sm:col-span-2">
    <label class="flex items-center gap-2 text-sm"><input type="checkbox" bind:checked={settings.pvp} /> PvP</label>
    <label class="flex items-center gap-2 text-sm"><input type="checkbox" bind:checked={settings.whitelist_enabled} /> Whitelist (only listed players can join)</label>
    <label class="flex items-center gap-2 text-sm">
      <input type="checkbox" bind:checked={settings.online_mode} /> Online mode (verify accounts with Mojang)
    </label>
    {#if meta.auto_sleep}
      <label class="flex items-start gap-2 text-sm">
        <input type="checkbox" class="mt-1" bind:checked={settings.auto_sleep} />
        <span>
          Auto sleep: stop after {meta.auto_sleep_idle} without players and start again when someone joins.
          <span class="text-zinc-500">The first join wakes the server, players reconnect after about 30 seconds.</span>
        </span>
      </label>
    {/if}
    {#if meta.playit.enabled}
      <label class="flex items-start gap-2 text-sm">
        <input type="checkbox" class="mt-1" bind:checked={settings.public} />
        <span>
          Public: reachable from the internet through a playit.gg tunnel.
          <span class="text-zinc-500">The public address shows up on the server page about a minute after saving.</span>
        </span>
      </label>
    {/if}
    {#if !settings.online_mode}
      <p class="text-sm text-amber-500">Without online mode anyone can join with any name. Enable the whitelist.</p>
    {/if}
    {#if settings.public && !settings.whitelist_enabled}
      <p class="text-sm text-amber-500">A public server without a whitelist can be joined by anyone who finds the address.</p>
    {/if}
  </div>
</div>
