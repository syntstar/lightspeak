import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { backoffMs, CLOSE, Gateway, HEARTBEAT_MS, type GatewayStatus } from "./gateway";

class FakeSocket {
  static OPEN = 1;
  readyState = 0;
  sent: string[] = [];
  onopen: (() => void) | null = null;
  onmessage: ((e: { data: unknown }) => void) | null = null;
  onclose: ((e: { code: number }) => void) | null = null;
  onerror: (() => void) | null = null;
  closedWith: number | null = null;
  constructor(public url: string) {}
  send(m: string) {
    this.sent.push(m);
  }
  close(code: number) {
    this.closedWith = code;
    this.readyState = 3;
  }
  // test helpers
  open() {
    this.readyState = 1;
    this.onopen?.();
  }
  recv(op: string, d?: unknown, s?: number) {
    this.onmessage?.({ data: JSON.stringify({ op, d, s }) });
  }
  drop(code: number) {
    this.readyState = 3;
    this.onclose?.({ code });
  }
}

function setup() {
  const sockets: FakeSocket[] = [];
  const events: [string, unknown][] = [];
  const statuses: GatewayStatus[] = [];
  let authLost = 0;
  const gw = new Gateway(
    "ws://x/ws",
    "tok",
    1,
    {
      onEvent: (op, d) => events.push([op, d]),
      onStatus: (s) => statuses.push(s),
      onAuthLost: () => authLost++,
    },
    (u) => {
      const s = new FakeSocket(u);
      sockets.push(s);
      return s as unknown as WebSocket;
    },
  );
  return { gw, sockets, events, statuses, authLost: () => authLost };
}

const last = <T>(a: T[]): T => a[a.length - 1]!;

beforeEach(() => {
  vi.useFakeTimers();
  vi.stubGlobal("WebSocket", FakeSocket);
});
afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

describe("backoff", () => {
  it("grows exponentially up to 30 s with jitter", () => {
    expect(backoffMs(0, () => 0)).toBe(1000);
    expect(backoffMs(1, () => 0)).toBe(2000);
    expect(backoffMs(4, () => 0)).toBe(16000);
    expect(backoffMs(10, () => 0)).toBe(30000);
    expect(backoffMs(10, () => 1)).toBe(39000);
  });
});

describe("Gateway", () => {
  it("identifies, then resumes with the last sequence after a drop", () => {
    const { gw, sockets, events, statuses } = setup();
    gw.start();
    const s1 = sockets[0]!;
    s1.open();
    expect(JSON.parse(s1.sent[0]!)).toEqual({ op: "identify", d: { token: "tok", protocolVersion: 1 } });
    s1.recv("ready", { sessionId: "S1" });
    s1.recv("message_create", { id: "1" }, 1);
    s1.recv("message_create", { id: "2" }, 2);
    expect(last(statuses)).toBe("ready");
    expect(events.map((e) => e[0])).toEqual(["ready", "message_create", "message_create"]);

    s1.drop(1006);
    expect(last(statuses)).toBe("reconnecting");
    vi.advanceTimersByTime(1400);
    const s2 = sockets[1]!;
    s2.open();
    expect(JSON.parse(s2.sent[0]!).d.resume).toEqual({ sessionId: "S1", lastSeq: 2 });
    s2.recv("resumed", { sessionId: "S1" });
    expect(last(statuses)).toBe("ready");
  });

  it("re-identifies immediately after invalid_session", () => {
    const { gw, sockets } = setup();
    gw.start();
    sockets[0]!.open();
    sockets[0]!.recv("ready", { sessionId: "S1" });
    sockets[0]!.drop(1006);
    vi.advanceTimersByTime(1400);
    const s2 = sockets[1]!;
    s2.open();
    s2.recv("invalid_session");
    s2.drop(1000);
    expect(sockets.length).toBe(3);
    sockets[2]!.open();
    expect(JSON.parse(sockets[2]!.sent[0]!).d.resume).toBeUndefined();
  });

  it("logs out on auth failure and revoked session", () => {
    for (const code of [CLOSE.authFail, CLOSE.revoked]) {
      const { gw, sockets, authLost } = setup();
      gw.start();
      sockets[0]!.open();
      sockets[0]!.drop(code);
      expect(authLost()).toBe(1);
      vi.advanceTimersByTime(60_000);
      expect(sockets.length).toBe(1);
    }
  });

  it("drops a dead connection after two missed pongs", () => {
    const { gw, sockets } = setup();
    gw.start();
    const s1 = sockets[0]!;
    s1.open();
    s1.recv("ready", { sessionId: "S1" });
    vi.advanceTimersByTime(HEARTBEAT_MS);
    s1.recv("pong");
    vi.advanceTimersByTime(HEARTBEAT_MS * 2);
    expect(s1.closedWith).toBeNull();
    vi.advanceTimersByTime(HEARTBEAT_MS);
    expect(s1.closedWith).toBe(4000);
    vi.advanceTimersByTime(1400);
    expect(sockets.length).toBe(2);
  });

  it("queues client ops while disconnected and flushes after ready", () => {
    const { gw, sockets } = setup();
    gw.start();
    gw.send("presence_set", { status: "idle" });
    gw.send("presence_set", { status: "dnd" });
    const s1 = sockets[0]!;
    s1.open();
    s1.recv("ready", { sessionId: "S1" });
    const ops = s1.sent.map((m) => JSON.parse(m) as { op: string; d: { status?: string } });
    expect(ops.filter((o) => o.op === "presence_set")).toEqual([
      { op: "presence_set", d: { status: "dnd" } },
    ]);
  });

  it("stop() prevents reconnects", () => {
    const { gw, sockets } = setup();
    gw.start();
    sockets[0]!.open();
    gw.stop();
    vi.advanceTimersByTime(60_000);
    expect(sockets.length).toBe(1);
  });
});
