// REST client for the currently selected server (see lib/server.ts; the app
// has no built-in server).
import type { AuthResponse, InvitePreview } from "./types";
import { isWeb } from "../platform";

/**
 * Browser client marker: the server accepts its HttpOnly session
 * cookie only together with this header (CSRF protection).
 */
export const WEB_HEADERS: Record<string, string> = isWeb ? { "X-Lightspeak-Client": "web" } : {};

export const PROTOCOL_VERSION = 1;

export class ApiError extends Error {
  constructor(
    public readonly code: string,
    public readonly status: number,
  ) {
    super(code);
    this.name = "ApiError";
  }
}

export function errorCode(e: unknown): string {
  if (e instanceof ApiError) return e.code;
  return "unknown";
}

export interface RequestOptions {
  method?: string;
  body?: unknown;
  raw?: BodyInit;
  headers?: Record<string, string>;
  signal?: AbortSignal;
}

export class Api {
  token: string | null = null;
  /** Browser client signed in: the HttpOnly cookie authenticates (no token in JS). */
  cookieSession = false;
  /** Called on 401 for an authenticated request (session revoked/expired). */
  onUnauthorized: (() => void) | null = null;

  /** Origin of the selected server ("" until one is chosen). */
  constructor(public base = "") {}

  url(path: string): string {
    return path.startsWith("http") ? path : this.base + path;
  }

  async request<T>(path: string, opts: RequestOptions = {}): Promise<T> {
    const headers: Record<string, string> = { ...WEB_HEADERS, ...opts.headers };
    if (this.token) headers.Authorization = `Bearer ${this.token}`;
    let body: BodyInit | undefined = opts.raw;
    if (opts.body !== undefined) {
      headers["Content-Type"] = "application/json";
      body = JSON.stringify(opts.body);
    }
    let res: Response;
    try {
      res = await fetch(this.base + "/api/v1" + path, {
        method: opts.method ?? (body ? "POST" : "GET"),
        headers,
        body,
        signal: opts.signal,
      });
    } catch (e) {
      if (e instanceof DOMException && e.name === "AbortError") throw e;
      throw new ApiError("network", 0);
    }
    if (res.status === 204) return undefined as T;
    const text = await res.text();
    let data: unknown = undefined;
    if (text) {
      try {
        data = JSON.parse(text);
      } catch {
        data = undefined;
      }
    }
    if (!res.ok) {
      const code = (data as { error?: { code?: string } } | undefined)?.error?.code ?? `http_${res.status}`;
      if (res.status === 401 && this.authed) this.onUnauthorized?.();
      throw new ApiError(code, res.status);
    }
    return data as T;
  }

  /** A session is active (bearer token or browser cookie). */
  get authed(): boolean {
    return !!this.token || this.cookieSession;
  }

  get = <T>(path: string, signal?: AbortSignal) => this.request<T>(path, { signal });
  post = <T>(path: string, body?: unknown) => this.request<T>(path, { method: "POST", body });
  patch = <T>(path: string, body?: unknown) => this.request<T>(path, { method: "PATCH", body });
  put = <T>(path: string, body?: unknown) => this.request<T>(path, { method: "PUT", body });
  del = <T>(path: string) => this.request<T>(path, { method: "DELETE" });

  // ---------- auth ----------

  invitePreview(code: string) {
    return this.get<InvitePreview>(`/invites/${encodeURIComponent(code)}`);
  }

  login(req: {
    username: string;
    password: string;
    deviceName: string;
    devicePublicKey?: string;
    deviceId?: string;
  }) {
    return this.post<AuthResponse>("/auth/login", req);
  }

  register(req: {
    inviteCode: string;
    username: string;
    displayName: string;
    password: string;
    deviceName: string;
    devicePublicKey: string;
  }) {
    return this.post<AuthResponse>("/auth/register", req);
  }

  /** Joins through a guest invite: no account, just a name. */
  guest(req: { inviteCode: string; displayName: string; deviceName: string; devicePublicKey: string }) {
    return this.post<AuthResponse>("/auth/guest", req);
  }
}
