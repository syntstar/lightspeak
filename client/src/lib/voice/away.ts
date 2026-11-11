// Away channel: after the space's configured idle time the client
// moves itself into the away channel. Idle means: no own voice activity and
// no input – system-wide in the app (GetLastInputInfo), inside the page in
// the browser. Runs only while connected to a voice channel.
import type { Channel, ID } from "../api/types";
import { awayTarget } from "../channelRules";

export interface AwayHost {
  channelId(): ID | null;
  channel(id: ID): Channel | undefined;
  spaceChannels(spaceId: ID): Channel[];
  /** Own stream running: someone presenting is not away. */
  streaming(): boolean;
  /** System idle seconds, or null where unknown (browser). */
  osIdle(): Promise<number | null>;
  move(target: Channel): void;
}

const CHECK_MS = 30_000;
const PAGE_EVENTS = ["pointerdown", "keydown", "wheel"] as const;

export class AwayWatch {
  private timer: ReturnType<typeof setInterval> | undefined;
  private lastActive = Date.now();
  private readonly onInput = (): void => this.active();

  constructor(
    private readonly host: AwayHost,
    private readonly pageInput: boolean,
  ) {}

  /** Own voice activity, joining or (browser) page input. */
  active(now = Date.now()): void {
    this.lastActive = now;
  }

  start(): void {
    this.active();
    if (this.timer) return;
    this.timer = setInterval(() => void this.check(), CHECK_MS);
    if (this.pageInput && typeof window !== "undefined")
      for (const ev of PAGE_EVENTS) window.addEventListener(ev, this.onInput, { passive: true });
  }

  stop(): void {
    clearInterval(this.timer);
    this.timer = undefined;
    if (this.pageInput && typeof window !== "undefined")
      for (const ev of PAGE_EVENTS) window.removeEventListener(ev, this.onInput);
  }

  /** The away channel to move to now, if any. */
  async due(now = Date.now()): Promise<Channel | null> {
    const cid = this.host.channelId();
    const c = cid ? this.host.channel(cid) : undefined;
    if (!c?.spaceId) return null;
    const target = awayTarget(this.host.spaceChannels(c.spaceId));
    if (!target || target.id === c.id || this.host.streaming()) return null;
    const ms = (target.awayAfter ?? 0) * 60_000;
    if (now - this.lastActive < ms) return null;
    const idle = await this.host.osIdle();
    if (idle !== null && idle * 1000 < ms) {
      this.active(now - idle * 1000); // input counts as activity, too
      return null;
    }
    return target;
  }

  async check(now = Date.now()): Promise<void> {
    const target = await this.due(now);
    if (target) this.host.move(target);
  }
}
