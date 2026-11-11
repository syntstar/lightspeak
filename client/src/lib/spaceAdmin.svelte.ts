// Space administration: menu entries for spaces,
// channel groups, channels and members, and which sheet is open. The sheets
// live in views/SpaceAdmin.svelte and are loaded on first use. The server
// enforces every permission; the checks here only hide entries that would
// fail anyway.
import UserPlus from "@lucide/svelte/icons/user-plus";
import Plus from "@lucide/svelte/icons/plus";
import Hash from "@lucide/svelte/icons/hash";
import Volume2 from "@lucide/svelte/icons/volume-2";
import FolderPlus from "@lucide/svelte/icons/folder-plus";
import Settings from "@lucide/svelte/icons/settings";
import LogOut from "@lucide/svelte/icons/log-out";
import Pencil from "@lucide/svelte/icons/pencil";
import ArrowUp from "@lucide/svelte/icons/arrow-up";
import ArrowDown from "@lucide/svelte/icons/arrow-down";
import Trash2 from "@lucide/svelte/icons/trash-2";
import Crown from "@lucide/svelte/icons/crown";
import UserMinus from "@lucide/svelte/icons/user-minus";
import Ban from "@lucide/svelte/icons/ban";
import type { Channel, ID } from "./api/types";
import { errorCode } from "./api/client";
import { app, api } from "./state/app.svelte";
import { t, terr } from "./i18n/index.svelte";
import { P, canManageRole, has, memberAccess, myAccess, outranks, type Access } from "./perms";
import { channelPerms, slowFor } from "./channelRules";
import type { MenuItem } from "./ui/menu.svelte";
import { reorder, type Move, type OrderItem } from "./channelOrder";
import { toast } from "./ui/toast.svelte";

export type ChannelType = "text" | "voice" | "category";
export type SettingsTab = "general" | "roles" | "members" | "bans";

export type AdminSheet =
  | { kind: "invite"; spaceId: ID }
  | { kind: "channel"; spaceId: ID; channel: Channel | null; type: ChannelType; parentId?: ID | null }
  | { kind: "settings"; spaceId: ID; tab: SettingsTab };

class SpaceAdmin {
  sheet = $state<AdminSheet | null>(null);
  open(s: AdminSheet): void {
    this.sheet = s;
  }
  close(): void {
    this.sheet = null;
  }
}
export const spaceAdmin = new SpaceAdmin();

/** The current user's access to a space (reactive). */
export const accessOf = (spaceId: ID): Access =>
  myAccess(app.spaces[spaceId], app.me?.id, !!app.me?.isServerAdmin);
export const can = (spaceId: ID, perm: number): boolean => has(accessOf(spaceId), perm);
/** Owner and server admins may delete or transfer a space. */
export const isOwnerLevel = (spaceId: ID): boolean => {
  const a = accessOf(spaceId);
  return a.owner || a.serverAdmin;
};
/** Access of another member (rank and permissions for moderation checks). */
export const accessOfMember = (spaceId: ID, userId: ID): Access =>
  memberAccess(
    app.spaces[spaceId],
    app.members[spaceId]?.find((m) => m.userId === userId),
  );
/** The current user may kick/ban/mute this member. */
export const canModerateMember = (spaceId: ID, userId: ID, perm: number): boolean =>
  userId !== app.me?.id && can(spaceId, perm) && outranks(accessOf(spaceId), accessOfMember(spaceId, userId));

/** Tab the settings sheet opens on, or null if the user can't manage anything. */
export function settingsTab(spaceId: ID): SettingsTab | null {
  if (can(spaceId, P.manageSpace) || isOwnerLevel(spaceId)) return "general";
  if (can(spaceId, P.manageRoles)) return "roles";
  if (can(spaceId, P.kick) || can(spaceId, P.ban)) return "members";
  return null;
}

function fail(err: unknown): void {
  toast(terr(errorCode(err)), "error");
}

async function leave(spaceId: ID): Promise<void> {
  const sp = app.spaces[spaceId];
  if (!sp || !app.me) return;
  if (!confirm(t("space.leaveConfirm", { name: sp.name }))) return;
  try {
    await api.del(`/spaces/${spaceId}/members/${app.me.id}`);
  } catch (err) {
    fail(err);
  }
}

const createChannel = (spaceId: ID, type: ChannelType, parentId: ID | null = null) =>
  spaceAdmin.open({ kind: "channel", spaceId, channel: null, type, parentId });

/** "Create …" entries for a channel group's or the channel list's context
 *  menu; empty without the permission. `only` limits them to one kind
 *  (right-click on "Text channels" offers just a text channel). */
export function createItems(
  spaceId: ID,
  opts: { only?: "text" | "voice"; parentId?: ID | null; noInvite?: boolean } = {},
): MenuItem[] {
  const items: MenuItem[] = [];
  if (can(spaceId, P.manageChannels)) {
    const parentId = opts.parentId ?? null;
    if (opts.only !== "voice")
      items.push({
        label: t("channel.createText"),
        icon: Hash,
        action: () => createChannel(spaceId, "text", parentId),
      });
    if (opts.only !== "text")
      items.push({
        label: t("channel.createVoice"),
        icon: Volume2,
        action: () => createChannel(spaceId, "voice", parentId),
      });
    if (!opts.only && !parentId)
      items.push({
        label: t("channel.createCategory"),
        icon: FolderPlus,
        action: () => createChannel(spaceId, "category"),
      });
  }
  if (!opts.noInvite && mayInvite(spaceId)) {
    if (items.length) items.push({ separator: true });
    items.push({
      label: t("invite.title"),
      icon: UserPlus,
      action: () => spaceAdmin.open({ kind: "invite", spaceId }),
    });
  }
  return items;
}

/** The space permission, unless the operator reserved invites (LS_INVITES). */
function mayInvite(spaceId: ID): boolean {
  if (app.me?.guest) return false;
  return can(spaceId, P.invite) && (app.limits.invites !== "admins" || !!app.me?.isServerAdmin);
}

/** Entries for the space header and the rail's context menu. */
export function spaceAdminItems(spaceId: ID): MenuItem[] {
  const a = accessOf(spaceId);
  if (!a.member) return [];
  const items: MenuItem[] = [];
  if (mayInvite(spaceId))
    items.push({
      label: t("invite.title"),
      icon: UserPlus,
      action: () => spaceAdmin.open({ kind: "invite", spaceId }),
    });
  if (has(a, P.manageChannels))
    items.push(
      { label: t("channel.create"), icon: Plus, action: () => createChannel(spaceId, "text") },
      {
        label: t("channel.createCategory"),
        icon: FolderPlus,
        action: () => createChannel(spaceId, "category"),
      },
    );
  const tab = settingsTab(spaceId);
  if (tab)
    items.push({
      label: t("spaceSettings.title"),
      icon: Settings,
      action: () => spaceAdmin.open({ kind: "settings", spaceId, tab }),
    });
  if (!a.owner) {
    items.push({ label: t("space.leave"), icon: LogOut, danger: true, action: () => void leave(spaceId) });
  }
  return items;
}

/** Channels of the same group (one category, or no category) or all
 *  categories, in display order. */
function siblings(c: Channel): Channel[] {
  const all = app.spaceChannels(c.spaceId!);
  if (c.kind === "category") return all.filter((o) => o.kind === "category");
  const cats = new Set(all.filter((o) => o.kind === "category").map((o) => o.id));
  const group = (o: Channel) => (o.parentId && cats.has(o.parentId) ? o.parentId : null);
  return all.filter((o) => o.kind !== "category" && group(o) === group(c));
}

/** Sends a new channel order; the server announces it to all. */
export async function applyOrder(spaceId: ID, body: OrderItem[] | null): Promise<void> {
  if (!body) return;
  try {
    await api.patch(`/spaces/${spaceId}/channels/order`, body);
  } catch (err) {
    fail(err);
  }
}

function moveTo(c: Channel, m: Move): Promise<void> {
  return applyOrder(c.spaceId!, reorder(app.spaceChannels(c.spaceId!), m));
}

async function move(c: Channel, dir: -1 | 1): Promise<void> {
  const list = siblings(c);
  const other = list[list.findIndex((o) => o.id === c.id) + dir];
  if (!other) return;
  await moveTo(
    c,
    c.kind === "category"
      ? { id: c.id, category: other.id, after: dir > 0 }
      : dir > 0
        ? { id: c.id, before: other.id, after: true }
        : { id: c.id, before: other.id },
  );
}

/** "Move to category" entries (keyboard alternative to drag & drop). */
function moveToItems(c: Channel): MenuItem[] {
  const all = app.spaceChannels(c.spaceId!);
  const cats = all.filter((o) => o.kind === "category");
  const current = c.parentId && cats.some((o) => o.id === c.parentId) ? c.parentId : null;
  const items: MenuItem[] = [{ heading: t("channel.moveTo") }];
  for (const cat of cats)
    items.push({
      label: cat.name,
      checked: current === cat.id,
      action: () => void moveTo(c, { id: c.id, into: cat.id }),
    });
  items.push({
    label: t("channel.uncategorized"),
    checked: current === null,
    action: () => void moveTo(c, { id: c.id, into: null }),
  });
  return items;
}

/** Admin entries for a space channel's or category's context menu. */
export function channelAdminItems(c: Channel): MenuItem[] {
  if (!c.spaceId || !can(c.spaceId, P.manageChannels)) return [];
  const list = siblings(c);
  const i = list.findIndex((o) => o.id === c.id);
  const cat = c.kind === "category";
  return [
    { separator: true },
    {
      label: cat ? t("channel.editCategory") : t("channel.edit"),
      icon: Pencil,
      action: () =>
        spaceAdmin.open({
          kind: "channel",
          spaceId: c.spaceId!,
          channel: c,
          type: cat ? "category" : c.kind === "voice" ? "voice" : "text",
        }),
    },
    { label: t("channel.moveUp"), icon: ArrowUp, disabled: i <= 0, action: () => void move(c, -1) },
    {
      label: t("channel.moveDown"),
      icon: ArrowDown,
      disabled: i < 0 || i >= list.length - 1,
      action: () => void move(c, 1),
    },
    ...(cat ? [] : [{ separator: true } as MenuItem, ...moveToItems(c), { separator: true } as MenuItem]),
    {
      label: cat ? t("channel.deleteCategory") : t("channel.delete"),
      icon: Trash2,
      danger: true,
      action: () => {
        const msg = cat
          ? t("channel.deleteCategoryConfirm", { name: c.name })
          : t("channel.deleteConfirm", { name: c.name });
        if (confirm(msg)) api.del(`/channels/${c.id}`).catch(fail);
      },
    },
  ];
}

/** Give or take a role (PUT/DELETE). */
export function setRole(spaceId: ID, userId: ID, roleId: ID, add: boolean): Promise<unknown> {
  const path = `/spaces/${spaceId}/members/${userId}/roles/${roleId}`;
  return (add ? api.put(path, {}) : api.del(path)).catch(fail);
}

/** Roles the current user may give to or take from this member. */
export function assignableRoles(spaceId: ID, userId: ID) {
  const sp = app.spaces[spaceId];
  const me = accessOf(spaceId);
  if (!sp || !has(me, P.manageRoles)) return [];
  if (userId !== app.me?.id && !outranks(me, accessOfMember(spaceId, userId))) return [];
  return sp.roles.filter((r) => canManageRole(me, r.position, r.permissions));
}

export async function transferOwner(spaceId: ID, userId: ID): Promise<void> {
  const sp = app.spaces[spaceId];
  const name = app.user(userId)?.displayName ?? "";
  if (!sp || !confirm(t("members.transferConfirm", { name, space: sp.name }))) return;
  try {
    await api.post(`/spaces/${spaceId}/owner`, { userId });
  } catch (err) {
    fail(err);
  }
}

/** Moderation entries for a member of the current space. */
export function memberAdminItems(spaceId: ID, userId: ID): MenuItem[] {
  const sp = app.spaces[spaceId];
  const m = app.members[spaceId]?.find((x) => x.userId === userId);
  if (!sp || !m) return [];
  const name = app.user(userId)?.displayName ?? "";
  const items: MenuItem[] = [];
  const roles = assignableRoles(spaceId, userId);
  if (roles.length) {
    items.push({ heading: t("roles.title") });
    for (const r of roles) {
      const on = m.roleIds?.includes(r.id) ?? false;
      items.push({ label: r.name, checked: on, action: () => void setRole(spaceId, userId, r.id, !on) });
    }
  }
  if (userId !== app.me?.id && isOwnerLevel(spaceId) && sp.ownerId !== userId) {
    if (items.length) items.push({ separator: true });
    items.push({
      label: t("members.makeOwner"),
      icon: Crown,
      action: () => void transferOwner(spaceId, userId),
    });
  }
  const kick = canModerateMember(spaceId, userId, P.kick);
  const ban = canModerateMember(spaceId, userId, P.ban);
  if ((kick || ban) && items.length) items.push({ separator: true });
  if (kick)
    items.push({
      label: t("members.kick"),
      icon: UserMinus,
      danger: true,
      action: () => {
        if (confirm(t("members.kickConfirm", { name })))
          api.del(`/spaces/${spaceId}/members/${userId}`).catch(fail);
      },
    });
  if (ban)
    items.push({
      label: t("members.ban"),
      icon: Ban,
      danger: true,
      action: () => {
        if (confirm(t("members.banConfirm", { name })))
          api.put(`/spaces/${spaceId}/bans/${userId}`, { reason: "" }).catch(fail);
      },
    });
  return items.length ? [{ separator: true }, ...items] : [];
}

/** Short tag next to a member's name: owner, server admin, their highest role or "guest". */
export function memberLabel(spaceId: ID, userId: ID, roleIds: readonly ID[], serverAdmin: boolean): string {
  const sp = app.spaces[spaceId];
  if (!sp) return "";
  if (sp.ownerId === userId) return t("members.owner");
  if (serverAdmin) return t("members.serverAdmin");
  const role = sp.roles.find((r) => roleIds.includes(r.id))?.name;
  return role ?? (app.user(userId)?.guest ? t("members.guest") : "");
}

export interface ChannelRights {
  send: boolean;
  attach: boolean;
  react: boolean;
  connect: boolean;
  speak: boolean;
  stream: boolean;
  /** Seconds of slow mode that apply to the current user. */
  slow: number;
}

/** What the current user may do in a channel; DMs allow all. */
export function channelRights(c: Channel): ChannelRights {
  if (!c.spaceId)
    return { send: true, attach: true, react: true, connect: true, speak: true, stream: true, slow: 0 };
  const a = accessOf(c.spaceId);
  const p = channelPerms(c, a, app.spaces[c.spaceId]?.roleIds ?? []);
  const on = (bit: number) => (p & bit) === bit;
  return {
    send: on(P.send),
    attach: on(P.send | P.attach),
    react: on(P.react),
    connect: on(P.connect),
    speak: on(P.speak),
    stream: on(P.stream),
    slow: slowFor(c, a),
  };
}
