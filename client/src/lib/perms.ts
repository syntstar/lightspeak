// Permissions, mirroring server/internal/server/roles.go. The
// server authorises every action; the client uses these rules only to hide
// what would fail anyway.
import type { ID, Member, Space } from "./api/types";

export const P = {
  manageSpace: 1 << 0,
  manageChannels: 1 << 1,
  manageRoles: 1 << 2,
  invite: 1 << 3,
  kick: 1 << 4,
  ban: 1 << 5,
  voiceModerate: 1 << 6,
  manageMessages: 1 << 7,
  mentionEveryone: 1 << 8,
  administrator: 1 << 9,
  // Channel permissions: per role, overridable per channel.
  connect: 1 << 10,
  speak: 1 << 11,
  stream: 1 << 12,
  send: 1 << 13,
  attach: 1 << 14,
  react: 1 << 15,
  // Private channels: without it a channel does not exist.
  view: 1 << 16,
} as const;
export type PermName = keyof typeof P;
export const ALL = (1 << 17) - 1;

/** Display order in the role editor, grouped. */
export const PERM_GROUPS: { key: "space" | "members" | "voice" | "text"; perms: PermName[] }[] = [
  { key: "space", perms: ["view", "manageSpace", "manageChannels", "manageRoles", "invite"] },
  { key: "members", perms: ["kick", "ban"] },
  { key: "voice", perms: ["connect", "speak", "stream", "voiceModerate"] },
  { key: "text", perms: ["send", "attach", "react", "mentionEveryone", "manageMessages"] },
];

/** What a channel of the kind may override, in display order. */
export const CHANNEL_PERMS: Record<"text" | "voice", PermName[]> = {
  voice: ["view", "connect", "speak", "stream"],
  text: ["view", "send", "attach", "react", "mentionEveryone"],
};

export const RANK_OWNER = 2 ** 40;
export const RANK_SERVER_ADMIN = 2 ** 41;

export interface Access {
  member: boolean;
  owner: boolean;
  serverAdmin: boolean;
  perms: number;
  rank: number;
}

const NONE: Access = { member: false, owner: false, serverAdmin: false, perms: 0, rank: 0 };

/** Access of a user with the given role IDs in a space. */
export function accessFor(
  sp: Pick<Space, "ownerId" | "roles" | "everyonePerms">,
  userId: ID,
  roleIds: readonly ID[],
  serverAdmin: boolean,
): Access {
  const owner = sp.ownerId === userId;
  let base = sp.everyonePerms ?? 0;
  let rank = 0;
  for (const r of sp.roles ?? []) {
    if (!roleIds.includes(r.id)) continue;
    base |= r.permissions;
    rank = Math.max(rank, r.position);
  }
  const perms = owner || serverAdmin || base & P.administrator ? ALL : base & ALL;
  if (serverAdmin) rank = RANK_SERVER_ADMIN;
  else if (owner) rank = RANK_OWNER;
  return { member: true, owner, serverAdmin, perms, rank };
}

export function myAccess(sp: Space | undefined, meId: ID | undefined, meServerAdmin: boolean): Access {
  if (!sp || !meId) return NONE;
  return accessFor(sp, meId, sp.roleIds ?? [], meServerAdmin);
}

export function memberAccess(sp: Space | undefined, m: Member | undefined): Access {
  if (!sp || !m) return NONE;
  return accessFor(sp, m.userId, m.roleIds ?? [], !!m.serverAdmin);
}

export const has = (a: Access, p: number): boolean => (a.perms & p) === p;

/** a may kick/ban/mute t. */
export const outranks = (a: Access, t: Access): boolean => a.rank > t.rank;

/** a may set or clear exactly these bits (only ones a holds). */
export const grantable = (a: Access, changed: number): boolean => (changed & ~a.perms) === 0;

/** a may edit, move, delete or hand out a role at this position with these permissions. */
export const canManageRole = (a: Access, position: number, permissions: number): boolean =>
  has(a, P.manageRoles) && position < a.rank && grantable(a, permissions);
