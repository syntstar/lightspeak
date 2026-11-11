// Server addresses: the app has no built-in server. Users pick one
// by address or invite link; everything server-specific is keyed by its origin.
import { ApiError, PROTOCOL_VERSION } from "./api/client";

export type SignupPolicy = "admin" | "invite" | "closed";

export interface ServerInfo {
  name: string;
  version: string;
  signup: SignupPolicy;
}

export interface ParsedInvite {
  code: string;
  /** Origin of the server the invite belongs to; null for a bare code. */
  server: string | null;
}

const LOOPBACK = new Set(["localhost", "127.0.0.1"]);
const CODE = /^[A-Za-z0-9]{8,32}$/;

const isLoopback = (hostname: string) => LOOPBACK.has(hostname);

/**
 * Turns user input ("chat.example.org", "https://chat.example.org/",
 * "127.0.0.1:8080", or a full link on that server) into an origin such as
 * "https://chat.example.org". Plain http is accepted for loopback hosts only,
 * so passwords never travel unencrypted over a network. Returns null when the
 * input is not a usable address.
 */
export function normalizeServer(input: string): string | null {
  let s = input.trim();
  if (!s || /\s/.test(s)) return null;
  if (!/^[a-z][a-z0-9+.-]*:\/\//i.test(s)) {
    const host = s.split(/[/:]/)[0]!.toLowerCase();
    s = (isLoopback(host) ? "http://" : "https://") + s;
  }
  let u: URL;
  try {
    u = new URL(s);
  } catch {
    return null;
  }
  if (u.username || u.password || !u.hostname) return null;
  if (u.protocol === "http:" && !isLoopback(u.hostname)) return null;
  if (u.protocol !== "https:" && u.protocol !== "http:") return null;
  return u.origin;
}

/** True when the input is an http:// address that is not loopback (refused). */
export function isInsecureAddress(input: string): boolean {
  try {
    const u = new URL(input.trim());
    return u.protocol === "http:" && !isLoopback(u.hostname);
  } catch {
    return false;
  }
}

/** Host shown to users ("chat.example.org" or "127.0.0.1:8080"). */
export function serverHost(origin: string): string {
  try {
    return new URL(origin).host;
  } catch {
    return origin;
  }
}

/**
 * Reads an invite from a bare code, a web link (`https://<host>/invite/<code>`)
 * or an app link (`lightspeak://invite/<host>/<code>`).
 */
export function parseInvite(input: string): ParsedInvite | null {
  const s = input.trim();
  if (CODE.test(s)) return { code: s, server: null };
  const app = /^lightspeak:\/\/invite\/([^/]+)\/([A-Za-z0-9]{8,32})\/?$/i.exec(s);
  if (app) {
    const server = normalizeServer(app[1]!);
    return server ? { code: app[2]!, server } : null;
  }
  let u: URL;
  try {
    u = new URL(s);
  } catch {
    return null;
  }
  // Only real web links: other schemes have an opaque origin ("null").
  if (u.protocol !== "https:" && u.protocol !== "http:") return null;
  const m = /^\/invite\/([A-Za-z0-9]{8,32})\/?$/.exec(u.pathname);
  if (!m || u.search || u.hash) return null;
  const server = normalizeServer(u.origin);
  return server ? { code: m[1]!, server } : null;
}

/** Web invite link for a server and code (what the landing page serves). */
export function inviteLink(server: string, code: string): string {
  return `${server}/invite/${code}`;
}

/**
 * Asks a server to describe itself and checks that it really is a compatible
 * Lightspeak server. Throws ApiError("network" | "not_lightspeak" |
 * "server_incompatible").
 */
export async function fetchServerInfo(origin: string, signal?: AbortSignal): Promise<ServerInfo> {
  let res: Response;
  try {
    res = await fetch(origin + "/api/v1/info", { signal });
  } catch (e) {
    if (e instanceof DOMException && e.name === "AbortError") throw e;
    throw new ApiError("network", 0);
  }
  const d = res.ok ? ((await res.json().catch(() => null)) as Record<string, unknown> | null) : null;
  if (!d || d.product !== "lightspeak") throw new ApiError("not_lightspeak", res.status);
  if (d.protocolVersion !== PROTOCOL_VERSION) throw new ApiError("server_incompatible", res.status);
  const signup: SignupPolicy = d.signup === "invite" || d.signup === "closed" ? d.signup : "admin";
  const name = typeof d.name === "string" && d.name.trim() ? d.name.trim().slice(0, 64) : serverHost(origin);
  return { name, version: typeof d.server === "string" ? d.server.slice(0, 32) : "", signup };
}
