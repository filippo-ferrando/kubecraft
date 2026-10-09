// Console socket client with automatic reconnect.

export interface TermMsg {
  type: "log" | "output" | "status" | "error" | "command";
  line?: string;
  command?: string;
  text?: string;
}

export class TermSocket {
  private ws: WebSocket | null = null;
  private closed = false;
  private retry = 0;
  private timer: ReturnType<typeof setTimeout> | undefined;

  constructor(
    private path: string,
    private onMessage: (m: TermMsg) => void,
    private onState: (connected: boolean) => void,
  ) {
    this.connect();
  }

  private connect() {
    const proto = location.protocol === "https:" ? "wss:" : "ws:";
    const ws = new WebSocket(`${proto}//${location.host}/api${this.path}`);
    this.ws = ws;
    ws.onopen = () => {
      this.retry = 0;
      this.onState(true);
    };
    ws.onmessage = (e) => {
      try {
        this.onMessage(JSON.parse(e.data));
      } catch {
        // Ignore malformed frames.
      }
    };
    ws.onclose = () => {
      this.onState(false);
      if (this.closed) return;
      const delay = Math.min(10000, 500 * 2 ** this.retry++);
      this.timer = setTimeout(() => this.connect(), delay);
    };
  }

  send(command: string): boolean {
    if (this.ws?.readyState !== WebSocket.OPEN) return false;
    this.ws.send(JSON.stringify({ type: "command", command }));
    return true;
  }

  close() {
    this.closed = true;
    clearTimeout(this.timer);
    this.ws?.close();
  }
}
