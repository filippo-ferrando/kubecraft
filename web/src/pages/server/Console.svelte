<script lang="ts">
  import { onMount } from "svelte";
  import "@xterm/xterm/css/xterm.css";
  import type { Server } from "../../lib/types";
  import { session } from "../../lib/session.svelte";
  import { TermSocket, type TermMsg } from "../../lib/terminal";

  let { server }: { server: Server; onchange: (s: Server) => void } = $props();

  const canType = $derived(session.user?.role === "admin" || session.user?.console_enabled);
  let el: HTMLDivElement;
  let input = $state("");
  let connected = $state(false);
  let history: string[] = [];
  let histPos = -1;
  let sock: TermSocket | null = null;
  let write: (s: string) => void = () => {};

  const c = { reset: "\x1b[0m", dim: "\x1b[2m", cyan: "\x1b[36m", yellow: "\x1b[33m", red: "\x1b[31m", green: "\x1b[32m" };

  // Basic highlighting for common log levels.
  function colorLog(line: string) {
    if (/\b(ERROR|SEVERE|FATAL)\b/.test(line)) return c.red + line + c.reset;
    if (/\bWARN(ING)?\b/.test(line)) return c.yellow + line + c.reset;
    return line;
  }

  function onMessage(m: TermMsg) {
    switch (m.type) {
      case "log":
        write(colorLog(m.line ?? ""));
        break;
      case "output":
        if (m.text) write(c.cyan + m.text.replace(/\n/g, "\r\n") + c.reset);
        break;
      case "error":
        write(c.red + "[kubecraft] " + (m.text ?? "") + c.reset);
        break;
      case "status":
        write(c.dim + "[kubecraft] " + (m.text ?? "") + c.reset);
        break;
    }
  }

  onMount(() => {
    let disposed = false;
    let cleanup = () => {};
    (async () => {
      // Loaded on demand to keep the main bundle small.
      const [{ Terminal }, { FitAddon }] = await Promise.all([import("@xterm/xterm"), import("@xterm/addon-fit")]);
      if (disposed) return;
      const term = new Terminal({
        convertEol: true,
        disableStdin: true,
        fontFamily: "ui-monospace, 'JetBrains Mono', monospace",
        fontSize: 13,
        scrollback: 5000,
        theme: { background: "#09090b", foreground: "#e4e4e7" },
      });
      const fit = new FitAddon();
      term.loadAddon(fit);
      term.open(el);
      fit.fit();
      write = (s) => term.writeln(s);
      const ro = new ResizeObserver(() => fit.fit());
      ro.observe(el);
      sock = new TermSocket(
        `/servers/${server.id}/${canType ? "console" : "logs"}`,
        onMessage,
        (ok) => {
          connected = ok;
          if (!ok) write(c.dim + "[kubecraft] disconnected, reconnecting..." + c.reset);
        },
      );
      cleanup = () => {
        ro.disconnect();
        sock?.close();
        term.dispose();
      };
    })();
    return () => {
      disposed = true;
      cleanup();
    };
  });

  function submit(e: SubmitEvent) {
    e.preventDefault();
    const cmd = input.trim();
    if (!cmd) return;
    if (!sock?.send(cmd)) {
      write(c.red + "[kubecraft] not connected" + c.reset);
      return;
    }
    write(c.green + "> " + cmd + c.reset);
    history = [cmd, ...history.filter((h) => h !== cmd)].slice(0, 50);
    histPos = -1;
    input = "";
  }

  function onKey(e: KeyboardEvent) {
    if (e.key === "ArrowUp" && histPos < history.length - 1) {
      histPos++;
      input = history[histPos];
      e.preventDefault();
    } else if (e.key === "ArrowDown") {
      histPos = Math.max(-1, histPos - 1);
      input = histPos >= 0 ? history[histPos] : "";
      e.preventDefault();
    }
  }

  const ready = $derived(server.status.state === "running");
</script>

<div class="overflow-hidden rounded-xl border border-zinc-200 bg-zinc-950 dark:border-zinc-800">
  <div class="h-[60vh] min-h-72 p-2" bind:this={el}></div>
  {#if canType}
    <form class="flex items-center gap-2 border-t border-zinc-800 p-2" onsubmit={submit}>
      <span class="font-mono text-sm text-brand-400">&gt;</span>
      <input
        class="flex-1 bg-transparent font-mono text-sm text-zinc-100 outline-none placeholder:text-zinc-600"
        placeholder={ready ? "Type a command, like list or say hello" : "The console works once the server is running"}
        maxlength="1000"
        autocomplete="off"
        spellcheck="false"
        bind:value={input}
        onkeydown={onKey}
        disabled={!connected}
      />
      <span class="h-2 w-2 rounded-full {connected ? 'bg-brand-500' : 'bg-zinc-600'}" title={connected ? "connected" : "disconnected"}></span>
    </form>
  {:else}
    <p class="border-t border-zinc-800 p-2 text-sm text-zinc-500">The console is disabled for your account, logs only.</p>
  {/if}
</div>
