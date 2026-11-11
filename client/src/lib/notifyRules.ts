// Notification rules, pure so they can be unit-tested.
// Per channel or space: level all / mentions / none, plus "muted".
// A channel setting overrides its space; defaults: DMs → all, spaces →
// mentions (only DMs, mentions and calls raise a toast by default).

export type NotifyLevel = "all" | "mentions" | "none";

export interface TargetSetting {
  level?: NotifyLevel;
  muted?: boolean;
}

export type NotifyMap = Record<string, TargetSetting>;

export interface ChannelRef {
  id: string;
  spaceId?: string;
  kind: string;
}

const isDM = (c: ChannelRef) => c.kind === "dm" || c.kind === "group_dm";

export function effectiveLevel(map: NotifyMap, c: ChannelRef): NotifyLevel {
  return (
    map[c.id]?.level ?? (c.spaceId ? map[c.spaceId]?.level : undefined) ?? (isDM(c) ? "all" : "mentions")
  );
}

export function isMuted(map: NotifyMap, c: ChannelRef): boolean {
  return !!map[c.id]?.muted || (!!c.spaceId && !!map[c.spaceId]?.muted);
}

export interface ToastInput {
  channel: ChannelRef;
  authorId: string;
  meId: string;
  mentionsMe: boolean;
  myStatus: string;
  /** The user is looking at this channel right now. */
  viewing: boolean;
  authorBlocked: boolean;
  enabled: boolean;
}

export function shouldToast(map: NotifyMap, x: ToastInput): boolean {
  if (!x.enabled || x.authorId === x.meId || x.viewing || x.authorBlocked) return false;
  if (x.myStatus === "dnd") return false;
  if (isMuted(map, x.channel)) return false;
  const level = effectiveLevel(map, x.channel);
  return level === "all" || (level === "mentions" && x.mentionsMe);
}

/** Validates the synced JSON (other devices may run other versions). */
export function sanitizeNotifyMap(raw: unknown): NotifyMap {
  const out: NotifyMap = {};
  if (!raw || typeof raw !== "object") return out;
  for (const [k, v] of Object.entries(raw as Record<string, unknown>)) {
    if (!/^\d{1,20}$/.test(k) || !v || typeof v !== "object") continue;
    const s = v as Record<string, unknown>;
    const t: TargetSetting = {};
    if (s.level === "all" || s.level === "mentions" || s.level === "none") t.level = s.level;
    if (s.muted === true) t.muted = true;
    if (t.level || t.muted) out[k] = t;
  }
  return out;
}

/** True while `now` lies in the daily quiet hours (from ≤ t < to, may wrap midnight). */
export function inQuietHours(q: { enabled: boolean; from: number; to: number }, now: Date): boolean {
  if (!q.enabled || q.from === q.to) return false;
  const m = now.getHours() * 60 + now.getMinutes();
  return q.from < q.to ? m >= q.from && m < q.to : m >= q.from || m < q.to;
}
