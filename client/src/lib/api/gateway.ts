// WebSocket gateway client: identify/resume, heartbeat, reconnect with
// exponential backoff + jitter, close-code handling (docs/ARCHITECTURE.md).

export type GatewayStatus = "connecting" | "ready" | "reconnecting" | "offline";

export interface GatewayHandlers {
  /** Every server event incl. ready/resumed. */
  onEvent(op: string, d: unknown): void;
  onStatus(status: GatewayStatus, retryInMs?: number): void;
  /** Authentication failed or session revoked: the app must log out. */
  onAuthLost(): void;
}

interface Frame {
  op: string;
  d?: unknown;
  s?: number;
}

export const HEARTBEAT_MS = 30_000;
export const CLOSE = { invalid: 4001, authFail: 4004, revoked: 4005, rateLimit: 4008, slow: 4009 } as const;

/** Backoff delay for attempt n (0-based): 1, 2, 4 … 30 s plus up to 30 % jitter. */
export function backoffMs(attempt: number, rand: () => number = Math.random): number {
  const base = Math.min(30_000, 1000 * 2 ** Math.min(attempt, 5));
  return Math.round(base + base * 0.3 * rand());
}

export type SocketFactory = (url: string) => WebSocket;

export class Gateway {
  private ws: WebSocket | null = null;
  private sessionId: string | null = null;
  private lastSeq = 0;
  private attempt = 0;
  private hbTimer: ReturnType<typeof setInterval> | undefined;
  private retryTimer: ReturnType<typeof setTimeout> | undefined;
  private awaitingPong = 0;
  private stopped = true;
  private queue: string[] = [];

  constructor(
    private readonly url: string,
    private readonly token: string,
    private readonly protocolVersion: number,
    private readonly h: GatewayHandlers,
    private readonly makeSocket: SocketFactory = (u) => new WebSocket(u),
  ) {}

  start(): void {
    this.stopped = false;
    this.connect();
  }

  stop(): void {
    this.stopped = true;
    clearTimeout(this.retryTimer);
    this.teardown(1000);
    this.sessionId = null;
    this.lastSeq = 0;
  }

  /** Reconnect right away (e.g. the OS reports the network is back). */
  nudge(): void {
    if (this.stopped || this.ws) return;
    clearTimeout(this.retryTimer);
    this.connect();
  }

  /** Sends a client op; queued while disconnected (latest per op wins). */
  send(op: string, d: unknown): void {
    const msg = JSON.stringify({ op, d });
    if (this.ws?.readyState === WebSocket.OPEN && this.identified) {
      this.ws.send(msg);
    } else {
      this.queue = this.queue.filter((q) => !q.startsWith(`{"op":"${op}"`));
      this.queue.push(msg);
    }
  }

  private identified = false;
  private freshNow = false;

  private connect(): void {
    this.h.onStatus(this.attempt === 0 && !this.sessionId ? "connecting" : "reconnecting");
    let ws: WebSocket;
    try {
      ws = this.makeSocket(this.url);
    } catch {
      this.scheduleRetry();
      return;
    }
    this.ws = ws;
    this.identified = false;
    ws.onopen = () => {
      const d: Record<string, unknown> = { token: this.token, protocolVersion: this.protocolVersion };
      if (this.sessionId) d.resume = { sessionId: this.sessionId, lastSeq: this.lastSeq };
      ws.send(JSON.stringify({ op: "identify", d }));
    };
    ws.onmessage = (ev) => this.onMessage(ev.data);
    ws.onclose = (ev) => this.onClose(ws, ev.code);
    ws.onerror = () => {
      // onclose follows and handles the retry
    };
  }

  private onMessage(data: unknown): void {
    if (typeof data !== "string") return;
    let f: Frame;
    try {
      f = JSON.parse(data) as Frame;
    } catch {
      return;
    }
    if (typeof f.s === "number" && f.s > 0) this.lastSeq = f.s;
    switch (f.op) {
      case "pong":
        this.awaitingPong = 0;
        return;
      case "ready":
        this.sessionId = (f.d as { sessionId: string }).sessionId;
        this.lastSeq = 0;
        this.onIdentified();
        break;
      case "resumed":
        this.onIdentified();
        break;
      case "invalid_session":
        // Server closes the socket next; reconnect without resume.
        this.sessionId = null;
        this.lastSeq = 0;
        this.freshNow = true;
        return;
    }
    this.h.onEvent(f.op, f.d);
  }

  private onIdentified(): void {
    this.identified = true;
    this.attempt = 0;
    this.awaitingPong = 0;
    clearInterval(this.hbTimer);
    this.hbTimer = setInterval(() => this.heartbeat(), HEARTBEAT_MS);
    const q = this.queue;
    this.queue = [];
    for (const m of q) this.ws?.send(m);
    this.h.onStatus("ready");
  }

  private heartbeat(): void {
    if (!this.ws || this.ws.readyState !== WebSocket.OPEN) return;
    if (this.awaitingPong >= 2) {
      // Two missed pongs: the connection is dead even if the OS has not noticed.
      this.teardown(4000);
      this.scheduleRetry();
      return;
    }
    this.awaitingPong++;
    this.ws.send('{"op":"ping"}');
  }

  private onClose(ws: WebSocket, code: number): void {
    if (ws !== this.ws) return; // stale socket
    this.teardown();
    if (this.stopped) return;
    if (code === CLOSE.authFail || code === CLOSE.revoked) {
      this.stopped = true;
      this.sessionId = null;
      this.h.onAuthLost();
      return;
    }
    if (code === CLOSE.invalid) {
      this.sessionId = null;
      this.lastSeq = 0;
    }
    // invalid_session → immediate fresh identify; everything else backs off.
    if (this.freshNow) {
      this.freshNow = false;
      this.connect();
      return;
    }
    this.scheduleRetry();
  }

  private scheduleRetry(): void {
    if (this.stopped) return;
    const delay = backoffMs(this.attempt++);
    this.h.onStatus(this.attempt > 3 ? "offline" : "reconnecting", delay);
    clearTimeout(this.retryTimer);
    this.retryTimer = setTimeout(() => this.connect(), delay);
  }

  private teardown(code?: number): void {
    clearInterval(this.hbTimer);
    this.identified = false;
    const ws = this.ws;
    this.ws = null;
    if (ws) {
      ws.onopen = ws.onmessage = ws.onclose = ws.onerror = null;
      if (
        code !== undefined &&
        (ws.readyState === WebSocket.OPEN || ws.readyState === WebSocket.CONNECTING)
      ) {
        try {
          ws.close(code === 4000 ? 4000 : 1000);
        } catch {
          // ignore
        }
      }
    }
  }
}
