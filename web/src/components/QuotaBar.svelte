<script lang="ts">
  import type { Quota } from "../lib/types";
  import { mem, cpu } from "../lib/format";
  import Meter from "./Meter.svelte";

  let { quota }: { quota: Quota } = $props();
  const l = $derived(quota.limits);
  const u = $derived(quota.usage);
  const of = (used: string, max: number | null, fmt: (n: number) => string) => (max === null ? `${used} / ∞` : `${used} / ${fmt(max)}`);
</script>

<div class="card grid grid-cols-2 gap-4 sm:grid-cols-4">
  <Meter label="Servers" value={u.servers} max={l.max_servers} text={of(String(u.servers), l.max_servers, String)} />
  <Meter label="RAM (running)" value={u.running_memory_mb} max={l.max_total_memory_mb} text={of(mem(u.running_memory_mb), l.max_total_memory_mb, mem)} />
  <Meter label="CPU (running)" value={u.running_cpu_m} max={l.max_total_cpu_m} text={of(cpu(u.running_cpu_m), l.max_total_cpu_m, cpu)} />
  <Meter label="Disk" value={u.storage_gb} max={l.max_total_storage_gb} text={of(`${u.storage_gb} GB`, l.max_total_storage_gb, (n) => `${n} GB`)} />
</div>
