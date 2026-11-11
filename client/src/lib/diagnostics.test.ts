import { describe, expect, it } from "vitest";
import { buildReport, redactPrefs, type ReportInput } from "./diagnostics";
import { inQuietHours } from "./notifyRules";
import { DEFAULT_PREFS, sanitizePrefs } from "./prefs.svelte";

const at = (h: number, m = 0) => new Date(2026, 10, 2, h, m);

describe("quiet hours", () => {
  it("handles ranges within a day and across midnight", () => {
    const day = { enabled: true, from: 9 * 60, to: 17 * 60 };
    expect(inQuietHours(day, at(8, 59))).toBe(false);
    expect(inQuietHours(day, at(9))).toBe(true);
    expect(inQuietHours(day, at(17))).toBe(false);
    const night = { enabled: true, from: 22 * 60, to: 8 * 60 };
    expect(inQuietHours(night, at(23, 30))).toBe(true);
    expect(inQuietHours(night, at(3))).toBe(true);
    expect(inQuietHours(night, at(8))).toBe(false);
    expect(inQuietHours(night, at(12))).toBe(false);
  });

  it("is off when disabled or empty", () => {
    expect(inQuietHours({ enabled: false, from: 0, to: 1439 }, at(3))).toBe(false);
    expect(inQuietHours({ enabled: true, from: 600, to: 600 }, at(10))).toBe(false);
  });

  it("sanitizes stored settings", () => {
    const p = sanitizePrefs({
      quiet: { enabled: true, from: 5000, to: -3 },
      streamCodec: "av1",
      memorySaver: false,
      taskbarBadge: "no",
      streamEncoder: { codec: "H264", impl: "MediaFoundationVideoEncodeAccelerator", hw: true, at: 5 },
    });
    expect(p.quiet).toEqual({ enabled: true, from: 1439, to: 0 });
    expect(p.streamCodec).toBe("auto");
    expect(p.memorySaver).toBe(false);
    expect(p.taskbarBadge).toBe(true);
    expect(p.streamEncoder?.hw).toBe(true);
  });
});

describe("diagnostics report", () => {
  const prefs = sanitizePrefs({
    ...structuredClone(DEFAULT_PREFS),
    server: "https://chat.example.org",
    servers: {
      "https://chat.example.org": {
        name: "Freunde",
        devices: { anna: { deviceId: "110552563270549504", publicKey: "A".repeat(44) } },
        knownKeys: { "110559232687865856": ["B".repeat(44)] },
      },
    },
    userVolumes: { "110559232687865856": 150 },
    audio: { inputDevice: "abcdef0123456789" },
  });
  const input: ReportInput = {
    createdAt: new Date(0),
    app: { version: "0.1.0", profile: "default", dev: false },
    system: { windows: "Windows 11 24H2 (26100.1)", webview2: "130.0" },
    userAgent: "Mozilla/5.0",
    locale: "de",
    server: { host: "chat.example.org", reachable: true, version: "0.1.0" },
    signedIn: true,
    gateway: "ready",
    voice: { status: "idle", encrypted: false, ping: null, streaming: false, watching: 0 },
    devices: [{ kind: "audioinput", label: "Mikrofon (USB)", isDefault: true }],
    stats: { workingSet: 150 * 1024 * 1024, privateBytes: 80 * 1024 * 1024, cpuPercent: 0.2, processes: 6 },
    prefs,
  };

  it("contains no ids, keys or device ids", () => {
    const r = buildReport(input);
    expect(r).not.toContain("110552563270549504");
    expect(r).not.toContain("110559232687865856");
    expect(r).not.toContain("A".repeat(44));
    expect(r).not.toContain("abcdef0123456789");
    expect(r).toContain("chat.example.org");
    expect(r).toContain("Mikrofon (USB) [default]");
    expect(r).toContain("150.0 MB");
  });

  it("keeps only counts of per-person maps", () => {
    const red = redactPrefs(prefs);
    expect(red.servers).toBe(1);
    expect(red.userVolumes).toBe(1);
    expect(red.server).toBe("chat.example.org");
  });
});
