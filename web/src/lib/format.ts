import type { Limits, Plan, State, Usage } from "./types";

export const typeLabels: Record<string, string> = {
  VANILLA: "Vanilla",
  PAPER: "Paper",
  PURPUR: "Purpur",
  FABRIC: "Fabric",
  FORGE: "Forge",
  NEOFORGE: "NeoForge",
};

// Server types grouped for the wizard.
export const typeGroups: { label: string; hint: string; types: string[] }[] = [
  { label: "Vanilla", hint: "Plain Minecraft", types: ["VANILLA"] },
  { label: "Plugins", hint: "Add plugins from Modrinth", types: ["PAPER", "PURPUR"] },
  { label: "Mods", hint: "Add mods from Modrinth", types: ["FABRIC", "FORGE", "NEOFORGE"] },
];

export function contentKind(type: string): "plugin" | "mod" | null {
  if (type === "PAPER" || type === "PURPUR") return "plugin";
  if (type === "FABRIC" || type === "FORGE" || type === "NEOFORGE") return "mod";
  return null;
}

export function count(n: number): string {
  if (n >= 1e6) return `${(n / 1e6).toFixed(1)}M`;
  if (n >= 1e3) return `${Math.round(n / 1e3)}k`;
  return String(n);
}

export const typeHints: Record<string, string> = {
  VANILLA: "The official server, no plugins or mods.",
  PAPER: "Fast, plugin support (Bukkit/Spigot). Good default.",
  PURPUR: "Paper fork with extra gameplay options.",
  FABRIC: "Lightweight mod loader.",
  FORGE: "Classic mod loader for big modpacks.",
  NEOFORGE: "Modern Forge fork.",
};

export function cpu(m: number): string {
  if (m > 0 && m < 100) return `${m}m CPU`;
  return m % 1000 === 0 ? `${m / 1000} CPU` : `${(m / 1000).toFixed(1)} CPU`;
}

export function mem(mb: number): string {
  return mb >= 1024 ? `${+(mb / 1024).toFixed(1)} GB` : `${mb} MB`;
}

export function planSummary(p: Plan): string {
  return `${cpu(p.cpu_limit_m)} · ${mem(p.memory_limit_mb)} RAM · ${p.storage_gb} GB disk`;
}

export const stateStyle: Record<State, { label: string; dot: string; text: string }> = {
  creating: { label: "Creating", dot: "bg-sky-500 animate-pulse", text: "text-sky-500" },
  starting: { label: "Starting", dot: "bg-amber-500 animate-pulse", text: "text-amber-500" },
  running: { label: "Running", dot: "bg-brand-500", text: "text-brand-500" },
  stopping: { label: "Stopping", dot: "bg-amber-500 animate-pulse", text: "text-amber-500" },
  stopped: { label: "Stopped", dot: "bg-zinc-400", text: "text-zinc-500" },
  error: { label: "Error", dot: "bg-red-500", text: "text-red-500" },
  crashloop: { label: "Crashing", dot: "bg-red-500 animate-pulse", text: "text-red-500" },
  deleting: { label: "Deleting", dot: "bg-zinc-400 animate-pulse", text: "text-zinc-500" },
  sleeping: { label: "Sleeping", dot: "bg-indigo-400", text: "text-indigo-400" },
};

export function bytes(n: number): string {
  if (n >= 1 << 30) return `${(n / (1 << 30)).toFixed(1)} GB`;
  if (n >= 1 << 20) return `${(n / (1 << 20)).toFixed(1)} MB`;
  if (n >= 1 << 10) return `${Math.round(n / (1 << 10))} KB`;
  return `${n} B`;
}

export function uptime(since?: string): string {
  if (!since) return "";
  const s = Math.max(0, Math.floor((Date.now() - new Date(since).getTime()) / 1000));
  const d = Math.floor(s / 86400);
  const h = Math.floor((s % 86400) / 3600);
  const m = Math.floor((s % 3600) / 60);
  if (d) return `${d}d ${h}h`;
  if (h) return `${h}h ${m}m`;
  return `${m}m`;
}

// fits explains why a plan does not fit the remaining quota, or returns "".
export function planBlocked(p: Plan, l: Limits, u: Usage): string {
  if (l.max_servers !== null && u.servers + 1 > l.max_servers) return `server limit reached (${u.servers}/${l.max_servers})`;
  if (l.max_total_storage_gb !== null && u.storage_gb + p.storage_gb > l.max_total_storage_gb)
    return `needs ${p.storage_gb} GB disk, ${l.max_total_storage_gb - u.storage_gb} GB left`;
  if (l.max_total_memory_mb !== null && u.running_memory_mb + p.memory_limit_mb > l.max_total_memory_mb)
    return `needs ${mem(p.memory_limit_mb)} RAM, ${mem(Math.max(0, l.max_total_memory_mb - u.running_memory_mb))} left`;
  if (l.max_total_cpu_m !== null && u.running_cpu_m + p.cpu_limit_m > l.max_total_cpu_m)
    return `needs ${cpu(p.cpu_limit_m)}, ${cpu(Math.max(0, l.max_total_cpu_m - u.running_cpu_m))} left`;
  return "";
}
