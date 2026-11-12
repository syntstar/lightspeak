import { Api, ApiError } from "../api/client";

/** Immutable credentials plus a lifetime check around every asynchronous step. */
export class KeySession {
  readonly api: Api;
  constructor(
    private readonly source: Api,
    private readonly current: () => boolean,
    private readonly signal: AbortSignal,
  ) {
    this.api = new Api(source.base);
    this.api.token = source.token;
    this.api.cookieSession = source.cookieSession;
  }

  check(): void {
    if (
      this.signal.aborted ||
      !this.current() ||
      this.source.base !== this.api.base ||
      this.source.token !== this.api.token ||
      this.source.cookieSession !== this.api.cookieSession
    ) {
      throw new ApiError("session_ended", 0);
    }
  }

  async run<T>(work: () => Promise<T>): Promise<T> {
    this.check();
    try {
      const result = await work();
      this.check();
      return result;
    } catch (e) {
      this.check();
      throw e;
    }
  }

  get<T>(path: string): Promise<T> {
    return this.run(() => this.api.get<T>(path, this.signal));
  }

  post<T>(path: string, body: unknown): Promise<T> {
    return this.run(() => this.api.request<T>(path, { method: "POST", body, signal: this.signal }));
  }
}
