<script lang="ts">
  import type { QuotaValues } from "../lib/types";

  // Empty inputs mean null: "use default" for users, "unlimited" for the default quota.
  let { value = $bindable(), emptyLabel }: { value: QuotaValues; emptyLabel: string } = $props();

  const fields: { key: keyof QuotaValues; label: string; unit: string }[] = [
    { key: "max_servers", label: "Servers", unit: "" },
    { key: "max_total_memory_mb", label: "RAM of running servers", unit: "MB" },
    { key: "max_total_cpu_m", label: "CPU of running servers", unit: "millicores" },
    { key: "max_total_storage_gb", label: "Disk", unit: "GB" },
  ];

  function set(key: keyof QuotaValues, raw: string) {
    const n = raw.trim() === "" ? null : Math.max(0, Math.floor(Number(raw)));
    value = { ...value, [key]: Number.isNaN(n) ? null : n };
  }
</script>

<div class="grid gap-3 sm:grid-cols-2">
  {#each fields as f (f.key)}
    <div>
      <label class="label" for={"q-" + f.key}>{f.label} {f.unit ? `(${f.unit})` : ""}</label>
      <input
        id={"q-" + f.key}
        class="input"
        type="number"
        min="0"
        placeholder={emptyLabel}
        value={value[f.key] ?? ""}
        oninput={(e) => set(f.key, e.currentTarget.value)}
      />
    </div>
  {/each}
</div>
