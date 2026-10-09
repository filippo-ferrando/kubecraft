<script lang="ts">
  let { label, value, max, text }: { label: string; value: number; max: number | null; text: string } = $props();
  const pct = $derived(max ? Math.min(100, Math.round((value / max) * 100)) : 0);
  const color = $derived(pct >= 90 ? "bg-red-500" : pct >= 70 ? "bg-amber-500" : "bg-brand-500");
</script>

<div class="min-w-0">
  <div class="mb-1 flex justify-between gap-2 text-xs text-zinc-500">
    <span>{label}</span>
    <span class="truncate">{text}</span>
  </div>
  <div class="h-1.5 overflow-hidden rounded-full bg-zinc-200 dark:bg-zinc-800">
    {#if max}
      <div class="h-full rounded-full transition-all {color}" style="width: {pct}%"></div>
    {/if}
  </div>
</div>
