<script lang="ts">
  let { address }: { address: string } = $props();
  let copied = $state(false);

  async function copy() {
    try {
      await navigator.clipboard.writeText(address);
    } catch {
      // Clipboard needs a secure context; select the text as a fallback.
      const r = document.createRange();
      r.selectNodeContents(document.getElementById("addr-" + address)!);
      getSelection()?.removeAllRanges();
      getSelection()?.addRange(r);
      return;
    }
    copied = true;
    setTimeout(() => (copied = false), 1500);
  }
</script>

<button
  type="button"
  onclick={(e) => {
    e.preventDefault();
    e.stopPropagation();
    copy();
  }}
  class="group inline-flex max-w-full items-center gap-2 rounded-md bg-zinc-100 px-2 py-1 font-mono text-xs hover:bg-zinc-200 dark:bg-zinc-800 dark:hover:bg-zinc-700"
  title="Copy address"
>
  <span id={"addr-" + address} class="truncate">{address}</span>
  <span class="text-zinc-500 group-hover:text-zinc-900 dark:group-hover:text-zinc-100">{copied ? "✓" : "⧉"}</span>
</button>
