// Typed wrappers around the Rust commands in src-tauri/src/lib.rs. In the
// browser build the commands the client needs there are answered
// by lib/web/web.ts instead; everything else stays desktop-only.
import type { TrayVoice } from "./trayVoice";
import { invoke } from "@tauri-apps/api/core";
import { listen } from "@tauri-apps/api/event";
import { isWeb } from "./platform";
export { inTauri, isWeb, hasStore } from "./platform";

// Browser implementations, loaded on first use. The literal mode check lets
// the bundler drop these modules from the desktop build entirely.
const webOnly = () => Promise.reject(new Error("web_only"));
const web = () => (import.meta.env.MODE === "web" ? import("./web/web") : webOnly());
const webInput = () => (import.meta.env.MODE === "web" ? import("./web/input") : webOnly());
const webSupport = () => (import.meta.env.MODE === "web" ? import("./web/support") : webOnly());

export interface AppInfo {
  profile: string;
  version: string;
  startMinimized: boolean;
  dev: boolean;
  computerName: string;
  /** Process start (Unix ms). */
  startedAt: number;
  /** Built with an updater key. */
  updater: boolean;
}

export interface UpdateInfo {
  version: string;
  notes: string | null;
  date: string | null;
}

export interface TrayState {
  labels: {
    open: string;
    mute: string;
    deafen: string;
    status: string;
    online: string;
    idle: string;
    dnd: string;
    invisible: string;
    quit: string;
    inVoice: string;
  };
  tooltip: string;
  muted: boolean;
  deafened: boolean;
  status: string;
  unread: boolean;
  inCall: boolean;
  /** Occupied voice channels. */
  voice: TrayVoice[];
}

export interface NativeToast {
  title: string;
  body: string;
  tag: string;
  args: string;
  actions?: { label: string; args: string }[];
  call?: boolean;
}

export type InputAction = "pushToTalk" | "toggleMute" | "toggleDeafen";

export interface InputConfig {
  bindings: { action: InputAction; keys: number[] }[];
  releaseDelayMs: number;
}

export type InputEvent =
  | { kind: "ptt"; active: boolean }
  | { kind: "toggle"; action: InputAction }
  | { kind: "elevatedForeground"; elevated: boolean }
  | { kind: "recorded"; keys: number[] };

export interface StoredSession {
  server: string;
  token: string;
  userId: string;
  deviceId: string;
}

export interface ProcStats {
  workingSet: number;
  privateBytes: number;
  /** Share of all cores since the previous call; null on the first call. */
  cpuPercent: number | null;
  processes: number;
}

export interface SystemInfo {
  windows: string;
  webview2: string;
}

export const native = {
  appInfo: async () =>
    isWeb ? (await web()).appInfo(__APP_VERSION__, import.meta.env.DEV) : invoke<AppInfo>("app_info"),
  /** Invite link (`https://<host>/invite/<code>`) from a deep link at startup. */
  takePendingInvite: async () =>
    isWeb ? (await web()).takePendingInvite(location.origin) : invoke<string | null>("take_pending_invite"),
  idleSeconds: () => invoke<number>("idle_seconds"),
  autostartGet: () => invoke<boolean>("autostart_get"),
  autostartSet: (enabled: boolean) => invoke<void>("autostart_set", { enabled }),
  setCloseToTray: (enabled: boolean) => invoke<void>("set_close_to_tray", { enabled }),
  trayUpdate: (state: TrayState) => invoke<void>("tray_update", { state }),
  notifyShow: async (toast: NativeToast) =>
    isWeb ? (await web()).notifyShow(toast) : invoke<void>("notify_show", { toast }),
  notifyClear: async (tag: string) =>
    isWeb ? (await web()).notifyClear(tag) : invoke<void>("notify_clear", { tag }),
  taskbarBadge: async (on: boolean, label: string) =>
    isWeb ? (await web()).taskbarBadge(on) : invoke<void>("taskbar_badge", { on, label }),
  /** Browser client: notification permission state / request (desktop: always "granted"). */
  notifyPermission: async (): Promise<"default" | "granted" | "denied" | "unsupported"> =>
    isWeb ? (await web()).notifyPermission() : "granted",
  notifyRequest: async (): Promise<"default" | "granted" | "denied" | "unsupported"> =>
    isWeb ? (await web()).notifyRequest() : "granted",
  /** Browser client: E2EE prerequisites present (desktop: always). */
  browserSupported: async (): Promise<boolean> => (isWeb ? (await webSupport()).browserSupported() : true),
  /** Toast / notification clicks (their `args`). */
  onToast: async (fn: (args: string) => void): Promise<void> => {
    if (isWeb) (await web()).onToast(fn);
    else await listen<string>("toast", (e) => fn(e.payload));
  },
  windowShow: () => invoke<void>("window_show"),
  quit: () => invoke<void>("app_quit"),
  settingsLoad: async () => (isWeb ? (await web()).settingsLoad() : invoke<string>("settings_load")),
  settingsSave: async (json: string) =>
    isWeb ? (await web()).settingsSave(json) : invoke<void>("settings_save", { json }),

  // Sessions and device keys are stored per server origin.
  async sessionGet(server: string): Promise<StoredSession | null> {
    if (isWeb) return (await web()).sessionGet(server);
    const raw = await invoke<string | null>("session_get", { server });
    if (!raw) return null;
    try {
      const s = JSON.parse(raw) as StoredSession;
      return s.token && s.server === server ? s : null;
    } catch {
      return null;
    }
  },
  sessionSet: async (s: StoredSession) =>
    isWeb
      ? (await web()).sessionSet(s)
      : invoke<void>("session_set", { server: s.server, json: JSON.stringify(s) }),
  sessionClear: async (server: string) =>
    isWeb ? (await web()).sessionClear(server) : invoke<void>("session_clear", { server }),

  // E2EE: Rust + Credential Manager on the desktop, WebCrypto + IndexedDB in
  // the browser (non-extractable private key). Same envelope format.
  /** Device public key (base64); creates the key pair when `create` is set. */
  deviceKey: async (server: string, create: boolean) =>
    isWeb ? (await web()).deviceKey(server, create) : invoke<string | null>("device_key", { server, create }),
  deviceKeyReset: async (server: string) =>
    isWeb ? (await web()).deviceKeyReset(server) : invoke<void>("device_key_reset", { server }),
  e2eeOpen: async (server: string, ciphertext: string) =>
    isWeb ? (await web()).e2eeOpen(server, ciphertext) : invoke<string>("e2ee_open", { server, ciphertext }),
  e2eeSeal: async (publicKey: string, mediaKey: string) =>
    isWeb
      ? (await web()).e2eeSeal(publicKey, mediaKey)
      : invoke<string>("e2ee_seal", { publicKey, mediaKey }),
  e2eeNewKey: async () => (isWeb ? (await web()).e2eeNewKey() : invoke<string>("e2ee_new_key")),
  keyFingerprint: async (publicKey: string) =>
    isWeb ? (await web()).keyFingerprint(publicKey) : invoke<string>("key_fingerprint", { publicKey }),

  /** Global push-to-talk / hotkey hook; runs only while in a call (browser: tab only). */
  inputStart: async (config: InputConfig) =>
    isWeb ? (await webInput()).inputStart(config) : invoke<void>("input_start", { config }),
  /** Records the next key combination (emits an "input" event of kind "recorded"). */
  inputRecord: async () => (isWeb ? (await webInput()).inputRecord() : invoke<void>("input_record")),
  inputStop: async () => (isWeb ? (await webInput()).inputStop() : invoke<void>("input_stop")),
  /** Hook events (push-to-talk, toggles, recorded keys); returns the unsubscribe. */
  onInput: async (fn: (ev: InputEvent) => void): Promise<() => void> =>
    isWeb ? (await webInput()).onInput(fn) : listen<InputEvent>("input", (e) => fn(e.payload)),
  keyName: async (code: number) =>
    isWeb ? (await web()).keyName(code) : invoke<string | null>("key_name", { code }),
  /** Trims the working sets of the process tree (after joining/leaving a call). */
  memoryTrim: () => invoke<void>("memory_trim"),

  setMemorySaver: (enabled: boolean) => invoke<void>("set_memory_saver", { enabled }),

  processStats: () => invoke<ProcStats>("process_stats"),
  logWrite: (level: "info" | "warn" | "error", message: string) =>
    invoke<void>("log_write", { level, message }),
  logsOpen: () => invoke<void>("logs_open"),
  systemInfo: () => invoke<SystemInfo>("system_info"),
  /** Saves the report to Downloads (and shows it); returns the file path. */
  diagnosticsExport: (report: string) => invoke<string>("diagnostics_export", { report }),
  updateCheck: () => invoke<UpdateInfo | null>("update_check"),
  updateInstall: () => invoke<void>("update_install"),
  /** Background download of the checked update for the next start. */
  updateDownload: () => invoke<void>("update_download"),
  updateDiscard: () => invoke<void>("update_discard"),
};
