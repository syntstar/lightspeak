// Browser implementations of the native commands the client needs
//. Loaded lazily from native.ts and only in the web build.
import type { AppInfo, NativeToast, StoredSession } from "../native";
import { deleteKey, loadKey, persistStorage, saveKey } from "./keystore";

const SETTINGS = "lightspeak.settings";
const SESSION = "lightspeak.session";
const startedAt = Date.now();

function lsGet(key: string): string | null {
  try {
    return localStorage.getItem(key);
  } catch {
    return null;
  }
}

function lsSet(key: string, value: string | null): void {
  try {
    if (value === null) localStorage.removeItem(key);
    else localStorage.setItem(key, value);
  } catch {
    // storage disabled (private mode with strict settings): stay in memory
  }
}

/** "Chrome · Windows" – the device name other sessions/devices are listed with. */
export function browserLabel(ua = navigator.userAgent): string {
  const browser = /Edg\//.test(ua)
    ? "Edge"
    : /OPR\//.test(ua)
      ? "Opera"
      : /Firefox\//.test(ua)
        ? "Firefox"
        : /Chrome\//.test(ua)
          ? "Chrome"
          : /Safari\//.test(ua)
            ? "Safari"
            : "Browser";
  const os = /Windows/.test(ua)
    ? "Windows"
    : /Android/.test(ua)
      ? "Android"
      : /iPhone|iPad/.test(ua)
        ? "iOS"
        : /Mac OS X/.test(ua)
          ? "macOS"
          : /Linux/.test(ua)
            ? "Linux"
            : "";
  return os ? `${browser} · ${os}` : browser;
}

export function appInfo(version: string, dev: boolean): AppInfo {
  return {
    profile: "default",
    version,
    startMinimized: false,
    dev,
    computerName: browserLabel(),
    startedAt,
    updater: false,
  };
}

/**
 * Invite code from the address (`/app/#invite=<code>`, set by the invite
 * page). The fragment never reaches the server; it is removed right away so
 * a reload or a shared screenshot does not carry it along.
 */
export function takePendingInvite(origin: string): string | null {
  const m = /^#invite=([A-Za-z0-9]{8,32})$/.exec(location.hash);
  if (!location.hash) return null;
  history.replaceState(null, "", location.pathname + location.search);
  return m ? `${origin}/invite/${m[1]}` : null;
}

export const settingsLoad = (): string => lsGet(SETTINGS) ?? "{}";
export const settingsSave = (json: string): void => lsSet(SETTINGS, json);

/**
 * Only non-secret session data is stored; the token itself lives in the
 * HttpOnly cookie (`token` stays empty). The gateway tells us when the
 * cookie is no longer valid.
 */
export function sessionGet(server: string): StoredSession | null {
  try {
    const s = JSON.parse(lsGet(SESSION) ?? "null") as Partial<StoredSession> | null;
    if (!s || s.server !== server || typeof s.userId !== "string" || typeof s.deviceId !== "string")
      return null;
    return { server, token: "", userId: s.userId, deviceId: s.deviceId };
  } catch {
    return null;
  }
}

export function sessionSet(s: StoredSession): void {
  lsSet(SESSION, JSON.stringify({ server: s.server, userId: s.userId, deviceId: s.deviceId }));
}

export function sessionClear(server: string): void {
  if (sessionGet(server)) lsSet(SESSION, null);
}

// ---------- E2EE (device key + sealed boxes) ----------

const box = () => import("./sealedBox");

export async function deviceKey(server: string, create: boolean): Promise<string | null> {
  const have = await loadKey(server);
  if (have) return have.publicKey;
  if (!create) return null;
  const sb = await box();
  if (!(await sb.supported())) throw new Error("e2ee_unsupported");
  const kp = await sb.generateDeviceKey();
  const publicKey = sb.b64(await sb.rawPublicKey(kp.publicKey));
  await saveKey(server, { privateKey: kp.privateKey, publicKey });
  persistStorage();
  return publicKey;
}

export const deviceKeyReset = (server: string): Promise<void> => deleteKey(server);

export async function e2eeOpen(server: string, ciphertext: string): Promise<string> {
  const k = await loadKey(server);
  if (!k) throw new Error("device_key_missing");
  const sb = await box();
  const key = await sb.open(k.privateKey, sb.unb64(k.publicKey), sb.unb64(ciphertext));
  try {
    if (key.length !== sb.KEY_LEN) throw new Error("envelope_invalid");
    return sb.b64(key);
  } finally {
    key.fill(0);
  }
}

export async function e2eeSeal(publicKey: string, mediaKey: string): Promise<string> {
  const sb = await box();
  const key = sb.unb64(mediaKey);
  if (key.length !== sb.KEY_LEN) throw new Error("media_key_invalid");
  try {
    return sb.b64(await sb.seal(sb.unb64(publicKey), key));
  } finally {
    key.fill(0);
  }
}

export async function e2eeNewKey(): Promise<string> {
  const sb = await box();
  const k = crypto.getRandomValues(new Uint8Array(sb.KEY_LEN));
  try {
    return sb.b64(k);
  } finally {
    k.fill(0);
  }
}

export async function keyFingerprint(publicKey: string): Promise<string> {
  const sb = await box();
  return sb.fingerprint(sb.unb64(publicKey));
}

// ---------- notifications ----------

const shown = new Map<string, Notification>();
let onClick: ((args: string) => void) | null = null;

export function onToast(fn: (args: string) => void): void {
  onClick = fn;
}

export function notifyPermission(): NotificationPermission | "unsupported" {
  return typeof Notification === "undefined" ? "unsupported" : Notification.permission;
}

export async function notifyRequest(): Promise<NotificationPermission | "unsupported"> {
  if (typeof Notification === "undefined") return "unsupported";
  if (Notification.permission !== "default") return Notification.permission;
  return Notification.requestPermission().catch(() => Notification.permission);
}

/**
 * Browser notification (shown by the OS while the tab is in the background).
 * Buttons such as "Accept" need a service worker – a click opens the tab
 * and the call/chat instead.
 */
export function notifyShow(t: NativeToast): void {
  if (notifyPermission() !== "granted") return;
  shown.get(t.tag)?.close();
  try {
    const n = new Notification(t.title, { body: t.body, tag: t.tag, icon: "favicon.svg", silent: true });
    n.onclick = () => {
      window.focus();
      n.close();
      if (t.args) onClick?.(t.args);
    };
    n.onclose = () => {
      if (shown.get(t.tag) === n) shown.delete(t.tag);
    };
    shown.set(t.tag, n);
  } catch {
    // some browsers refuse page notifications (Android): in-app toasts remain
  }
}

export function notifyClear(tag: string): void {
  shown.get(tag)?.close();
  shown.delete(tag);
}

/** Unread marker in the tab title (the desktop app uses a taskbar badge). */
export function taskbarBadge(on: boolean): void {
  const base = document.title.replace(/^• /, "");
  document.title = on ? `• ${base}` : base;
}

// ---------- keys (in-tab push-to-talk) ----------

/** Mouse buttons use the same codes as the native hook (MOUSE_BASE + n). */
export const MOUSE_BASE = 0x1_0000;

/** KeyboardEvent → Windows virtual-key code as the native hook records it. */
export function keyCodeOf(e: KeyboardEvent): number | null {
  const k = e.keyCode; // legacy but equal to the Windows VK code in all browsers on Windows
  if (!k || k > 0xfe) return null;
  return k;
}

/** MouseEvent.button → hook code (middle, back, forward only). */
export function mouseCodeOf(e: MouseEvent): number | null {
  if (e.button === 1) return MOUSE_BASE + 3;
  if (e.button === 3) return MOUSE_BASE + 4;
  if (e.button === 4) return MOUSE_BASE + 5;
  return null;
}

const NAMES: Record<number, string> = {
  0x08: "Backspace",
  0x09: "Tab",
  0x0d: "Enter",
  0x10: "Shift",
  0x11: "Ctrl",
  0x12: "Alt",
  0x13: "Pause",
  0x14: "Caps Lock",
  0x1b: "Esc",
  0x20: "Space",
  0x21: "Page Up",
  0x22: "Page Down",
  0x23: "End",
  0x24: "Home",
  0x25: "←",
  0x26: "↑",
  0x27: "→",
  0x28: "↓",
  0x2d: "Insert",
  0x2e: "Delete",
  0x5b: "Win",
  0x5d: "Menu",
  0x6a: "Num *",
  0x6b: "Num +",
  0x6d: "Num -",
  0x6e: "Num ,",
  0x6f: "Num /",
  0x90: "Num Lock",
  0x91: "Scroll Lock",
  [MOUSE_BASE + 3]: "Mouse 3",
  [MOUSE_BASE + 4]: "Mouse 4",
  [MOUSE_BASE + 5]: "Mouse 5",
};

export function keyName(code: number): string | null {
  if (NAMES[code]) return NAMES[code]!;
  if ((code >= 0x30 && code <= 0x39) || (code >= 0x41 && code <= 0x5a)) return String.fromCharCode(code);
  if (code >= 0x60 && code <= 0x69) return `Num ${code - 0x60}`;
  if (code >= 0x70 && code <= 0x87) return `F${code - 0x6f}`;
  return null;
}
