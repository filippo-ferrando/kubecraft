// poll calls fn now and every ms while the tab is visible. Returns a stop function.
export function poll(fn: () => Promise<void> | void, ms: number): () => void {
  let timer: ReturnType<typeof setTimeout> | undefined;
  let stopped = false;
  const tick = async () => {
    if (stopped) return;
    if (!document.hidden) {
      try {
        await fn();
      } catch {
        // Errors are shown by the caller; keep polling.
      }
    }
    if (!stopped) timer = setTimeout(tick, ms);
  };
  tick();
  return () => {
    stopped = true;
    clearTimeout(timer);
  };
}
