<script lang="ts">
  import type { Component } from "svelte";
  import { onMount } from "svelte";
  import Nav from "./components/Nav.svelte";
  import Login from "./pages/Login.svelte";
  import Account from "./pages/Account.svelte";
  import Dashboard from "./pages/Dashboard.svelte";
  import NotFound from "./pages/NotFound.svelte";
  import CreateServer from "./pages/CreateServer.svelte";
  import ServerPage from "./pages/ServerPage.svelte";
  import Admin from "./pages/Admin.svelte";
  import { session, loadSession } from "./lib/session.svelte";
  import { route, match, navigate } from "./lib/router.svelte";
  import { errorText } from "./lib/api";

  type Route = { pattern: string; page: Component<any>; admin?: boolean };

  const routes: Route[] = [
    { pattern: "/", page: Dashboard },
    { pattern: "/account", page: Account },
    { pattern: "/servers/new", page: CreateServer },
    { pattern: "/servers/:id", page: ServerPage },
    { pattern: "/servers/:id/:tab", page: ServerPage },
    { pattern: "/admin", page: Admin, admin: true },
    { pattern: "/admin/:tab", page: Admin, admin: true },
  ];

  let loadError = $state("");

  onMount(() => {
    loadSession().catch((e) => (loadError = errorText(e)));
  });

  const current = $derived.by(() => {
    for (const r of routes) {
      const params = match(r.pattern, route.path);
      if (params) return { ...r, params };
    }
    return { pattern: "", page: NotFound as Component<any>, params: {} };
  });

  // Redirects for anonymous users, forced password changes, and non-admins.
  $effect(() => {
    if (!session.loaded) return;
    if (!session.user) {
      if (route.path !== "/login") {
        const next = route.path === "/" ? "" : "?next=" + encodeURIComponent(route.path);
        navigate("/login" + next, true);
      }
      return;
    }
    if (route.path === "/login") navigate("/", true);
    else if (session.user.must_change_password && route.path !== "/account") navigate("/account", true);
    else if (current.admin && session.user.role !== "admin") navigate("/", true);
  });
</script>

{#if loadError}
  <div class="p-8 text-center text-red-500">Could not reach the server: {loadError}</div>
{:else if !session.loaded}
  <div class="p-8 text-center text-zinc-500">Loading...</div>
{:else if !session.user}
  <Login />
{:else}
  <Nav />
  <main class="mx-auto max-w-6xl px-4 py-6">
    {#key current.pattern.startsWith("/servers/:id") ? "server:" + current.params.id : current.pattern.startsWith("/admin") ? "admin" : route.path}
      <current.page {...current.params} />
    {/key}
  </main>
{/if}
