// Diagnostics report (Settings → About): what helps to debug audio, stream
// and connection problems — and nothing that identifies people or grants
// access. No tokens, no message content, no user/channel ids, no keys; the
// Rust side scrubs long opaque strings once more before writing the file.
import type { Prefs } from "./prefs.svelte";

export interface ReportInput {
  createdAt: Date;
  app: { version: string; profile: string; dev: boolean };
  system: { windows: string; webview2: string } | null;
  userAgent: string;
  locale: string;
  server: { host: string; reachable: boolean | null; version: string | null } | null;
  signedIn: boolean;
  gateway: string;
  voice: { status: string; encrypted: boolean; ping: number | null; streaming: boolean; watching: number };
  devices: { kind: string; label: string; isDefault: boolean }[];
  stats: { workingSet: number; privateBytes: number; cpuPercent: number | null; processes: number } | null;
  prefs: Prefs;
}

/** Local settings without per-person data (only counts for maps keyed by ids). */
export function redactPrefs(p: Prefs): Record<string, unknown> {
  const { servers, server, userVolumes, localMutes, streamVolumes, collapsed, audio, ...rest } = plainCopy(p);
  return {
    ...rest,
    server: server ? hostOf(server) : "",
    servers: Object.keys(servers).length,
    userVolumes: Object.keys(userVolumes).length,
    localMutes: Object.keys(localMutes).length,
    streamVolumes: Object.keys(streamVolumes).length,
    collapsed: Object.keys(collapsed).length,
    // Device ids are random per origin, but labels are what helps; keep the mode.
    audio: {
      ...audio,
      inputDevice: audio.inputDevice ? "(chosen)" : "",
      outputDevice: audio.outputDevice ? "(chosen)" : "",
    },
  };
}

/** Deep copy that also works on Svelte state proxies. */
function plainCopy<T>(v: T): T {
  return JSON.parse(JSON.stringify(v)) as T;
}

export function hostOf(origin: string): string {
  try {
    return new URL(origin).host;
  } catch {
    return "";
  }
}

const mb = (n: number) => `${(n / (1024 * 1024)).toFixed(1)} MB`;

export function buildReport(r: ReportInput): string {
  const lines: string[] = [];
  const kv = (k: string, v: unknown) =>
    lines.push(`${k.padEnd(18)} ${v === null || v === undefined || v === "" ? "–" : String(v)}`);
  lines.push("Lightspeak diagnostics", "======================", "");
  kv("created", r.createdAt.toISOString());
  kv("version", `${r.app.version}${r.app.dev ? " (dev)" : ""}`);
  kv("profile", r.app.profile);
  kv("windows", r.system?.windows);
  kv("webview2", r.system?.webview2);
  kv("user agent", r.userAgent);
  kv("ui language", r.locale);
  lines.push("");
  kv("server", r.server?.host);
  kv("server reachable", r.server?.reachable);
  kv("server version", r.server?.version);
  kv("signed in", r.signedIn);
  kv("gateway", r.gateway);
  lines.push("");
  kv("voice", r.voice.status);
  kv("voice e2ee", r.voice.encrypted);
  kv("voice ping", r.voice.ping === null ? null : `${r.voice.ping} ms`);
  kv("streaming", r.voice.streaming);
  kv("watching", r.voice.watching);
  lines.push("");
  if (r.stats) {
    kv("memory (WS)", mb(r.stats.workingSet));
    kv("private bytes", mb(r.stats.privateBytes));
    kv("cpu", r.stats.cpuPercent === null ? null : `${r.stats.cpuPercent.toFixed(1)} %`);
    kv("processes", r.stats.processes);
    lines.push("");
  }
  lines.push("audio devices:");
  if (!r.devices.length) lines.push("  –");
  for (const d of r.devices)
    lines.push(`  ${d.kind.padEnd(12)} ${d.label || "(no label)"}${d.isDefault ? " [default]" : ""}`);
  lines.push("", "settings:", JSON.stringify(redactPrefs(r.prefs), null, 2));
  return lines.join("\n");
}
