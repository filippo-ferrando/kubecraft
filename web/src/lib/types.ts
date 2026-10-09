export interface User {
  id: string;
  username: string;
  role: "admin" | "user";
  disabled: boolean;
  must_change_password: boolean;
  console_enabled: boolean;
  created_at: string;
}

export interface Me {
  user: User;
  csrf_token: string;
}

export interface Plan {
  id: string;
  name: string;
  cpu_request_m: number;
  cpu_limit_m: number;
  memory_limit_mb: number;
  storage_gb: number;
  active: boolean;
}

export interface Settings {
  motd: string;
  difficulty: string;
  gamemode: string;
  max_players: number;
  view_distance: number;
  whitelist_enabled: boolean;
  whitelist: string[];
  ops: string[];
  pvp: boolean;
  seed: string;
  level_type: string;
  online_mode: boolean;
  modrinth_projects: string[];
  auto_sleep: boolean;
}

export interface CatalogProject {
  id: string;
  slug: string;
  title: string;
  description: string;
  icon_url: string;
  downloads: number;
  categories: string[];
}

export type State = "creating" | "starting" | "running" | "stopping" | "stopped" | "error" | "crashloop" | "deleting" | "sleeping";

export interface Status {
  state: State;
  message: string;
  restart_required: boolean;
  started_at?: string;
  node?: string;
}

export interface LiveStats {
  players?: { online: number; max: number; names: string[] };
  cpu_m?: number;
  memory_mb?: number;
  updated_at: string;
}

export interface Server {
  id: string;
  owner_id: string;
  owner_username?: string;
  slug: string;
  display_name: string;
  address: string;
  plan: Plan;
  type: string;
  version: string;
  settings: Settings;
  desired_state: "running" | "stopped" | "deleting";
  status: Status;
  live?: LiveStats;
  created_at: string;
  updated_at: string;
}

export interface Limits {
  max_servers: number | null;
  max_total_cpu_m: number | null;
  max_total_memory_mb: number | null;
  max_total_storage_gb: number | null;
}

export interface Usage {
  servers: number;
  storage_gb: number;
  running_cpu_m: number;
  running_memory_mb: number;
}

export interface Quota {
  limits: Limits;
  usage: Usage;
}

export interface Meta {
  server_base_domain: string;
  types: string[];
  difficulties: string[];
  gamemodes: string[];
  level_types: string[];
  default_settings: Settings;
  jvm_heap_ratio: number;
  auto_sleep: boolean;
  auto_sleep_idle: string;
  files: boolean;
}

export interface FileEntry {
  name: string;
  path: string;
  dir: boolean;
  size: number;
  modified: string;
  editable: boolean;
}

export interface VersionList {
  latest: string;
  versions: { id: string; release_time: string }[];
}

export interface QuotaValues {
  max_servers: number | null;
  max_total_cpu_m: number | null;
  max_total_memory_mb: number | null;
  max_total_storage_gb: number | null;
}

export interface AdminUser extends User {
  quota: QuotaValues;
  limits: Limits;
  usage: Usage;
}

export interface AuditEntry {
  id: number;
  username: string;
  action: string;
  target: string;
  details: Record<string, unknown>;
  created_at: string;
}
