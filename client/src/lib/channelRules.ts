// Channel settings and permissions, mirroring
// server/internal/server/channelrules.go. The server enforces them; the
// client only shows what applies.
import type { Channel, ID, Override, Space } from "./api/types";
import { CHANNEL_PERMS, P, has, type Access } from "./perms";

/** Everything that only exists inside a channel. */
const CHANNEL_BITS = [...CHANNEL_PERMS.voice, ...CHANNEL_PERMS.text].reduce((m, p) => m | P[p], 0);

export type ChannelMode = "open" | "away";
export type AudioLevel = "speech" | "high" | "music";

export const SLOWMODE_STEPS = [0, 5, 30, 60, 300] as const; // seconds
export const AWAY_STEPS = [0, 5, 15, 30, 60] as const; // minutes
export const LIMIT_STEPS = [0, 2, 3, 4, 5, 6, 8, 10, 12, 15, 20, 25, 50, 99] as const;
export const AUDIO_LEVELS: readonly AudioLevel[] = ["speech", "high", "music"];

export const modeOf = (c: Pick<Channel, "mode">): ChannelMode => (c.mode === "away" ? "away" : "open");
export const audioOf = (c: Pick<Channel, "audio"> | undefined): AudioLevel =>
  c?.audio === "high" || c?.audio === "music" ? c.audio : "speech";

/** Effective permissions in a space channel: base, then the @everyone
 *  override, then all role overrides together (allow beats deny).
 *  Administrators are never restricted; nobody talks in the away channel. */
export function channelPerms(c: Channel, a: Access, roleIds: readonly ID[]): number {
  let p = a.perms;
  if (!has(a, P.administrator)) {
    const ov = c.overrides ?? [];
    const everyone = ov.find((o) => o.roleId === c.spaceId);
    if (everyone) p = (p & ~everyone.deny) | everyone.allow;
    let allow = 0;
    let deny = 0;
    for (const o of ov)
      if (o.roleId !== c.spaceId && roleIds.includes(o.roleId)) {
        allow |= o.allow;
        deny |= o.deny;
      }
    p = (p & ~deny) | allow;
  }
  // Everyone sees and may enter the away channel (idle people are moved
  // there), nobody talks. Without View a channel grants nothing.
  if (modeOf(c) === "away") p = (p & ~(P.speak | P.stream)) | P.connect | P.view;
  else if (c.kind === "category") p |= P.view;
  else if (!(p & P.view)) p &= ~CHANNEL_BITS;
  return p;
}

/** Slow mode applies (moderators of messages or channels are exempt). */
export const slowFor = (c: Channel, a: Access): number =>
  c.slowmode && !has(a, P.manageMessages) && !has(a, P.manageChannels) ? c.slowmode : 0;

/** The @everyone override denies p: a read-only or stage-like channel. */
export const restrictedForAll = (c: Channel, p: number): boolean =>
  !!c.overrides?.some((o) => o.roleId === c.spaceId && (o.deny & p) === p);

/** Override of one role, or an empty one. */
export const overrideOf = (c: Pick<Channel, "overrides">, roleId: ID): Override =>
  c.overrides?.find((o) => o.roleId === roleId) ?? { roleId, allow: 0, deny: 0 };

export type TriState = "deny" | "inherit" | "allow";
export const triOf = (o: Override, bit: number): TriState =>
  o.allow & bit ? "allow" : o.deny & bit ? "deny" : "inherit";
export function setTri(o: Override, bit: number, v: TriState): Override {
  return {
    roleId: o.roleId,
    allow: v === "allow" ? o.allow | bit : o.allow & ~bit,
    deny: v === "deny" ? o.deny | bit : o.deny & ~bit,
  };
}

/** Roles that may write / speak despite a channel-wide restriction, highest first. */
export function allowedRoles(sp: Space | undefined, c: Channel, p: number): string[] {
  if (!sp) return [];
  const ids = (c.overrides ?? []).filter((o) => (o.allow & p) === p).map((o) => o.roleId);
  return (sp.roles ?? []).filter((r) => ids.includes(r.id)).map((r) => r.name);
}

/** The away channel of a space, if it moves idle people. */
export function awayTarget(channels: readonly Channel[]): Channel | undefined {
  return channels.find((c) => c.kind === "voice" && modeOf(c) === "away" && (c.awayAfter ?? 0) > 0);
}
