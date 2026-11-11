// Local, per-device preferences (settings.json in the profile directory via
// Rust). Never contains secrets. Changes apply immediately and are saved
// debounced.
import type { LocalePref } from "./i18n/index.svelte";
import { native, hasStore } from "./native";
import { normalizeServer } from "./server";
import { DEFAULT_STREAM, sanitizeStream, type StreamPrefs } from "./voice/streamPresets";

export interface AudioPrefs {
  inputDevice: string; // "" = Windows default
  outputDevice: string;
  inputVolume: number; // 0..200 %
  outputVolume: number; // 0..200 %
  mode: "vad" | "ptt";
  vadAuto: boolean;
  vadThreshold: number; // dBFS, -100..0
  pttKeys: number[];
  pttDelay: number; // ms, 0..1000
  muteKeys: number[];
  deafenKeys: number[];
  noise: "off" | "standard" | "strong";
  echoCancellation: boolean;
  autoGainControl: boolean;
  sounds: boolean;
}

export const DEFAULT_AUDIO: AudioPrefs = {
  inputDevice: "",
  outputDevice: "",
  inputVolume: 100,
  outputVolume: 100,
  mode: "vad",
  vadAuto: true,
  vadThreshold: -50,
  pttKeys: [],
  pttDelay: 200,
  muteKeys: [],
  deafenKeys: [],
  noise: "standard",
  echoCancellation: true,
  autoGainControl: true,
  sounds: true,
};

export type ThemePref = "system" | "light" | "dark";
export type Accent = "blue" | "purple" | "pink" | "orange" | "green" | "graphite";
export const ACCENTS: readonly Accent[] = ["blue", "purple", "pink", "orange", "green", "graphite"];

/** Everything tied to one server (ids and keys are only unique per server). */
export interface ServerPrefs {
  /** Display name from the server's /info (cached for the server picker). */
  name: string;
  lastChannel: Record<string, string>;
  lastSpace: string | null;
  /** Device id per username + the public key it was registered with, so a
   *  re-login reuses the device only while the key pair is unchanged. */
  devices: Record<string, { deviceId: string; publicKey: string }>;
  /** Trust on first use: device public keys seen per contact (user id). */
  knownKeys: Record<string, string[]>;
}

export const DEFAULT_SERVER_PREFS: ServerPrefs = {
  name: "",
  lastChannel: {},
  lastSpace: null,
  devices: {},
  knownKeys: {},
};

const MAX_SERVERS = 20;

/** Daily quiet hours (minutes since midnight, local time); may span midnight. */
export interface QuietHours {
  enabled: boolean;
  from: number;
  to: number;
}

export type StreamCodec = "auto" | "h264" | "vp8";

/** Encoder seen in the last own stream (Settings → Streaming). */
export interface EncoderInfo {
  codec: string;
  impl: string;
  /** powerEfficientEncoder from getStats (null = not reported). */
  hw: boolean | null;
  at: number;
}

export interface Prefs {
  /** Origin of the selected server ("" = none chosen yet). */
  server: string;
  /** Per-server data, keyed by origin. */
  servers: Record<string, ServerPrefs>;
  language: LocalePref;
  theme: ThemePref;
  accent: Accent;
  chatFontSize: number;
  compact: boolean;
  mica: boolean;
  reduceMotion: boolean;
  closeToTray: boolean;
  showMembers: boolean;
  /** Windows toasts for DMs, mentions and calls. */
  toasts: boolean;
  /** Show the message text inside toasts. */
  toastPreview: boolean;
  /** Alert when someone in my voice channel starts streaming (9.6). */
  streamAlerts: boolean;
  /** Overlay dot on the taskbar button while something unread waits. */
  taskbarBadge: boolean;
  /** No toasts or sounds during these hours (like the DND status). */
  quiet: QuietHours;
  /** Lower WebView2 memory target while minimised/in the tray. */
  memorySaver: boolean;
  /** Download updates in the background, install at the next start. */
  autoUpdate: boolean;
  streamCodec: StreamCodec;
  streamEncoder: EncoderInfo | null;
  collapsed: Record<string, boolean>;
  audio: AudioPrefs;
  /** Per-user playback volume in percent (0..200), default 100. */
  userVolumes: Record<string, number>;
  /** Users muted locally (only for me). */
  localMutes: Record<string, true>;
  /** Last screen share choice (dialog). */
  stream: StreamPrefs;
  /** Per-streamer stream audio volume in percent (0..200), default 100. */
  streamVolumes: Record<string, number>;
}

export const DEFAULT_PREFS: Prefs = {
  server: "",
  servers: {},
  language: "system",
  theme: "system",
  accent: "blue",
  chatFontSize: 14,
  compact: false,
  mica: true,
  reduceMotion: false,
  closeToTray: true,
  showMembers: true,
  toasts: true,
  toastPreview: true,
  streamAlerts: true,
  taskbarBadge: true,
  quiet: { enabled: false, from: 22 * 60, to: 8 * 60 },
  memorySaver: true,
  autoUpdate: true,
  streamCodec: "auto",
  streamEncoder: null,
  collapsed: {},
  audio: structuredClone(DEFAULT_AUDIO),
  userVolumes: {},
  localMutes: {},
  stream: { ...DEFAULT_STREAM },
  streamVolumes: {},
};

const clampNum = (v: unknown, min: number, max: number): number | undefined =>
  typeof v === "number" && Number.isFinite(v) ? Math.min(max, Math.max(min, Math.round(v))) : undefined;

const keyList = (v: unknown): number[] | undefined =>
  Array.isArray(v) && v.length <= 4 && v.every((k) => Number.isInteger(k) && k > 0 && k < 0x20000)
    ? (v as number[])
    : undefined;

export function sanitizeAudio(raw: unknown): AudioPrefs {
  const a = structuredClone(DEFAULT_AUDIO);
  if (!raw || typeof raw !== "object") return a;
  const r = raw as Record<string, unknown>;
  for (const k of ["inputDevice", "outputDevice"] as const)
    if (typeof r[k] === "string") a[k] = r[k].slice(0, 200);
  a.inputVolume = clampNum(r.inputVolume, 0, 200) ?? a.inputVolume;
  a.outputVolume = clampNum(r.outputVolume, 0, 200) ?? a.outputVolume;
  if (r.mode === "vad" || r.mode === "ptt") a.mode = r.mode;
  a.vadThreshold = clampNum(r.vadThreshold, -100, 0) ?? a.vadThreshold;
  a.pttDelay = clampNum(r.pttDelay, 0, 1000) ?? a.pttDelay;
  for (const k of ["pttKeys", "muteKeys", "deafenKeys"] as const) a[k] = keyList(r[k]) ?? a[k];
  if (r.noise === "off" || r.noise === "standard" || r.noise === "strong") a.noise = r.noise;
  for (const k of ["vadAuto", "echoCancellation", "autoGainControl", "sounds"] as const) {
    if (typeof r[k] === "boolean") a[k] = r[k];
  }
  return a;
}

export function sanitizeServerPrefs(raw: unknown): ServerPrefs {
  const p: ServerPrefs = structuredClone(DEFAULT_SERVER_PREFS);
  if (!raw || typeof raw !== "object") return p;
  const r = raw as Record<string, unknown>;
  if (typeof r.name === "string") p.name = r.name.slice(0, 64);
  if (r.lastChannel && typeof r.lastChannel === "object") {
    for (const [k, v] of Object.entries(r.lastChannel)) if (typeof v === "string") p.lastChannel[k] = v;
  }
  if (typeof r.lastSpace === "string") p.lastSpace = r.lastSpace;
  if (r.knownKeys && typeof r.knownKeys === "object") {
    for (const [k, v] of Object.entries(r.knownKeys as Record<string, unknown>)) {
      if (!/^\d+$/.test(k) || !Array.isArray(v)) continue;
      const list = v.filter((x): x is string => typeof x === "string" && /^[A-Za-z0-9+/=]{40,64}$/.test(x));
      if (list.length) p.knownKeys[k] = list.slice(-20);
    }
  }
  if (r.devices && typeof r.devices === "object") {
    for (const [k, v] of Object.entries(r.devices as Record<string, unknown>)) {
      const d = v as { deviceId?: unknown; publicKey?: unknown } | null;
      if (d && typeof d.deviceId === "string" && typeof d.publicKey === "string")
        p.devices[k] = { deviceId: d.deviceId, publicKey: d.publicKey };
    }
  }
  return p;
}

/** Data of the given (default: selected) server; created on first use. */
export function serverPrefs(origin = prefs.server): ServerPrefs {
  if (!prefs.servers[origin]) prefs.servers[origin] = structuredClone(DEFAULT_SERVER_PREFS);
  return prefs.servers[origin]!;
}

/** Merges stored JSON over the defaults, dropping invalid values. */
export function sanitizePrefs(raw: unknown): Prefs {
  const p: Prefs = structuredClone(DEFAULT_PREFS);
  if (!raw || typeof raw !== "object") return p;
  const r = raw as Record<string, unknown>;
  if (r.language === "de" || r.language === "en" || r.language === "system") p.language = r.language;
  if (r.theme === "light" || r.theme === "dark" || r.theme === "system") p.theme = r.theme;
  if (typeof r.accent === "string" && (ACCENTS as readonly string[]).includes(r.accent))
    p.accent = r.accent as Accent;
  if (typeof r.chatFontSize === "number" && r.chatFontSize >= 12 && r.chatFontSize <= 16)
    p.chatFontSize = Math.round(r.chatFontSize);
  for (const k of [
    "compact",
    "mica",
    "reduceMotion",
    "closeToTray",
    "showMembers",
    "toasts",
    "toastPreview",
    "streamAlerts",
    "taskbarBadge",
    "memorySaver",
    "autoUpdate",
  ] as const) {
    if (typeof r[k] === "boolean") p[k] = r[k];
  }
  if (r.collapsed && typeof r.collapsed === "object") {
    for (const [k, v] of Object.entries(r.collapsed)) if (v === true) p.collapsed[k] = true;
  }
  if (r.servers && typeof r.servers === "object") {
    for (const [k, v] of Object.entries(r.servers as Record<string, unknown>).slice(0, MAX_SERVERS)) {
      if (normalizeServer(k) === k) p.servers[k] = sanitizeServerPrefs(v);
    }
  }
  if (typeof r.server === "string" && normalizeServer(r.server) === r.server) p.server = r.server;
  p.audio = sanitizeAudio(r.audio);
  for (const key of ["userVolumes", "streamVolumes"] as const) {
    const m = r[key];
    if (!m || typeof m !== "object") continue;
    for (const [k, v] of Object.entries(m)) {
      const n = clampNum(v, 0, 200);
      if (n !== undefined && /^\d+$/.test(k)) p[key][k] = n;
    }
  }
  p.stream = sanitizeStream(r.stream);
  if (r.streamCodec === "auto" || r.streamCodec === "h264" || r.streamCodec === "vp8")
    p.streamCodec = r.streamCodec;
  const e = r.streamEncoder as Record<string, unknown> | null | undefined;
  if (e && typeof e === "object" && typeof e.impl === "string" && typeof e.codec === "string")
    p.streamEncoder = {
      codec: e.codec.slice(0, 32),
      impl: e.impl.slice(0, 64),
      hw: typeof e.hw === "boolean" ? e.hw : null,
      at: clampNum(e.at, 0, Number.MAX_SAFE_INTEGER) ?? 0,
    };
  const q = r.quiet as Record<string, unknown> | undefined;
  if (q && typeof q === "object") {
    if (typeof q.enabled === "boolean") p.quiet.enabled = q.enabled;
    p.quiet.from = clampNum(q.from, 0, 1439) ?? p.quiet.from;
    p.quiet.to = clampNum(q.to, 0, 1439) ?? p.quiet.to;
  }
  if (r.localMutes && typeof r.localMutes === "object") {
    for (const [k, v] of Object.entries(r.localMutes))
      if (v === true && /^\d+$/.test(k)) p.localMutes[k] = true;
  }
  return p;
}

export const prefs: Prefs = $state(structuredClone(DEFAULT_PREFS));
if (import.meta.env.DEV) Object.assign(globalThis, { __lvPrefs: prefs });

let saveTimer: ReturnType<typeof setTimeout> | undefined;

export async function loadPrefs(): Promise<void> {
  if (!hasStore) return;
  try {
    Object.assign(prefs, sanitizePrefs(JSON.parse(await native.settingsLoad())));
  } catch {
    // corrupt file: keep defaults
  }
}

export function savePrefs(): void {
  if (!hasStore) return;
  clearTimeout(saveTimer);
  saveTimer = setTimeout(() => {
    void native.settingsSave(JSON.stringify($state.snapshot(prefs))).catch(() => {});
  }, 400);
}
