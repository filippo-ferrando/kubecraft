<script lang="ts">
  import { link, route, navigate } from "../lib/router.svelte";
  import { session, logout, isAdmin } from "../lib/session.svelte";
  import { isDark, toggleTheme } from "../lib/theme";

  let dark = $state(isDark());
  let menuOpen = $state(false);

  const items = $derived([
    { href: "/", label: "Servers" },
    ...(isAdmin() ? [{ href: "/admin", label: "Admin" }] : []),
    { href: "/account", label: "Account" },
  ]);

  function active(href: string) {
    return href === "/" ? route.path === "/" || route.path.startsWith("/servers") : route.path.startsWith(href);
  }

  async function doLogout() {
    await logout();
    navigate("/login", true);
  }
</script>

<header class="sticky top-0 z-20 border-b border-zinc-200 bg-white/80 backdrop-blur dark:border-zinc-800 dark:bg-zinc-950/80">
  <div class="mx-auto flex h-14 max-w-6xl items-center gap-4 px-4">
    <a href="/" use:link class="flex items-center gap-2 font-semibold">
      <img src="/favicon.svg" alt="" class="h-6 w-6" />
      kubecraft
    </a>
    <nav class="hidden gap-1 sm:flex">
      {#each items as it (it.href)}
        <a
          href={it.href}
          use:link
          class="rounded-md px-3 py-1.5 text-sm {active(it.href)
            ? 'bg-zinc-100 font-medium dark:bg-zinc-800'
            : 'text-zinc-600 hover:text-zinc-900 dark:text-zinc-400 dark:hover:text-zinc-100'}">{it.label}</a
        >
      {/each}
    </nav>
    <div class="ml-auto flex items-center gap-2">
      <button class="btn-secondary px-2" title="Toggle theme" onclick={() => (dark = toggleTheme())}>
        {dark ? "☀" : "☾"}
      </button>
      <span class="hidden text-sm text-zinc-500 sm:inline">{session.user?.username}</span>
      <button class="btn-secondary hidden sm:inline-flex" onclick={doLogout}>Log out</button>
      <button class="btn-secondary px-2 sm:hidden" aria-label="Menu" onclick={() => (menuOpen = !menuOpen)}>☰</button>
    </div>
  </div>
  {#if menuOpen}
    <nav class="flex flex-col gap-1 border-t border-zinc-200 px-4 py-2 sm:hidden dark:border-zinc-800">
      {#each items as it (it.href)}
        <a href={it.href} use:link onclick={() => (menuOpen = false)} class="rounded-md px-3 py-2 text-sm">{it.label}</a>
      {/each}
      <button class="rounded-md px-3 py-2 text-left text-sm" onclick={doLogout}>Log out</button>
    </nav>
  {/if}
</header>
