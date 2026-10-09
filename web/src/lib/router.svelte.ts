// Minimal history router. Routes are matched in App.svelte.

export const route = $state({ path: location.pathname, query: new URLSearchParams(location.search) });

export function navigate(to: string, replace = false) {
  const url = new URL(to, location.origin);
  if (replace) history.replaceState(null, "", url);
  else history.pushState(null, "", url);
  route.path = url.pathname;
  route.query = url.searchParams;
  window.scrollTo(0, 0);
}

window.addEventListener("popstate", () => {
  route.path = location.pathname;
  route.query = new URLSearchParams(location.search);
});

// Action for <a href> elements so clicks stay inside the SPA.
export function link(node: HTMLAnchorElement) {
  const onClick = (e: MouseEvent) => {
    if (e.defaultPrevented || e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
    const href = node.getAttribute("href");
    if (!href || !href.startsWith("/")) return;
    e.preventDefault();
    navigate(href);
  };
  node.addEventListener("click", onClick);
  return { destroy: () => node.removeEventListener("click", onClick) };
}

// match returns route params when pattern (like "/servers/:id") matches path.
export function match(pattern: string, path: string): Record<string, string> | null {
  const p = pattern.split("/").filter(Boolean);
  const s = path.split("/").filter(Boolean);
  if (p.length !== s.length) return null;
  const params: Record<string, string> = {};
  for (let i = 0; i < p.length; i++) {
    if (p[i].startsWith(":")) params[p[i].slice(1)] = decodeURIComponent(s[i]);
    else if (p[i] !== s[i]) return null;
  }
  return params;
}
