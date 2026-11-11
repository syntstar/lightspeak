// Context menu entries for a user (member list, message author, friends,
// DM list, voice channel). Voice entries (volume, local mute, moderation)
// appear when they apply.
import MessageSquare from "@lucide/svelte/icons/message-square";
import UserPlus from "@lucide/svelte/icons/user-plus";
import UserMinus from "@lucide/svelte/icons/user-minus";
import Ban from "@lucide/svelte/icons/ban";
import Copy from "@lucide/svelte/icons/copy";
import AtSign from "@lucide/svelte/icons/at-sign";
import type { ID } from "./api/types";
import { errorCode } from "./api/client";
import { app } from "./state/app.svelte";
import { t, terr } from "./i18n/index.svelte";
import type { MenuItem } from "./ui/menu.svelte";
import { toast } from "./ui/toast.svelte";
import VolumeX from "@lucide/svelte/icons/volume-x";
import MonitorPlay from "@lucide/svelte/icons/monitor-play";
import MonitorOff from "@lucide/svelte/icons/monitor-off";
import MicOff from "@lucide/svelte/icons/mic-off";
import Mic from "@lucide/svelte/icons/mic";
import PhoneOff from "@lucide/svelte/icons/phone-off";
import ArrowRightLeft from "@lucide/svelte/icons/arrow-right-left";
import { voice } from "./voice/voice.svelte";
import { P } from "./perms";
import { canModerateMember } from "./spaceAdmin.svelte";

/** Volume, local mute and (for admins) moderation of a user in voice. */
export function voiceMenuItems(userId: ID): MenuItem[] {
  const items: MenuItem[] = [];
  const vs = app.voiceStates[userId];
  if (vs?.streaming) {
    const watching = voice.stream.isWatching(userId);
    items.push(
      {
        label: watching ? t("stream.stopWatching") : t("stream.watch"),
        icon: watching ? MonitorOff : MonitorPlay,
        action: () => (watching ? voice.stream.unwatch(userId) : void voice.stream.watch(userId)),
      },
      ...(watching && userId !== app.me?.id
        ? [
            {
              slider: t("stream.volume"),
              value: voice.stream.streamVolume(userId),
              min: 0,
              max: 200,
              step: 5,
              reset: 100,
              format: (v: number) => `${v} %`,
              onchange: (v: number) => voice.stream.setStreamVolume(userId, v),
            },
          ]
        : []),
      { separator: true },
    );
  }
  if (userId === app.me?.id) return items;
  if (vs?.channelId && vs.channelId === voice.channelId) {
    items.push(
      {
        slider: t("voice.userVolume"),
        value: voice.userVolume(userId),
        min: 0,
        max: 200,
        step: 5,
        reset: 100,
        format: (v) => `${v} %`,
        onchange: (v) => voice.setUserVolume(userId, v),
      },
      {
        label: t("voice.localMute"),
        icon: VolumeX,
        checked: voice.isLocallyMuted(userId),
        action: () => voice.toggleLocalMute(userId),
      },
      { separator: true },
    );
  }
  const ch = vs?.channelId ? app.channels[vs.channelId] : undefined;
  const sp = ch?.spaceId ? app.spaces[ch.spaceId] : undefined;
  if (ch && sp && ch.kind === "voice") {
    if (canModerateMember(sp.id, userId, P.voiceModerate)) {
      const others = app.spaceChannels(sp.id).filter((c) => c.kind === "voice" && c.id !== ch.id);
      items.push(
        { heading: t("voice.moderation") },
        vs?.serverMuted
          ? { label: t("voice.serverUnmute"), icon: Mic, action: () => run(voice.moderate(userId, "unmute")) }
          : { label: t("voice.serverMute"), icon: MicOff, action: () => run(voice.moderate(userId, "mute")) },
        ...others.map((c) => ({
          label: t("voice.moveTo", { name: c.name }),
          icon: ArrowRightLeft,
          action: () => run(voice.moderate(userId, "move", c.id)),
        })),
        {
          label: t("voice.disconnectUser"),
          icon: PhoneOff,
          danger: true,
          action: () => run(voice.moderate(userId, "disconnect")),
        },
        { separator: true },
      );
    }
  }
  return items;
}

function run(p: Promise<unknown>, ok?: string): void {
  p.then(() => ok && toast(ok, "success")).catch((e: unknown) => toast(terr(errorCode(e)), "error"));
}

export function userMenuItems(userId: ID, opts: { onMention?: () => void } = {}): MenuItem[] {
  const items: MenuItem[] = voiceMenuItems(userId);
  if (userId === app.me?.id) {
    items.push({
      label: t("user.copyId"),
      icon: Copy,
      action: () =>
        void navigator.clipboard.writeText(userId).then(() => toast(t("toast.copied"), "success")),
    });
    return items;
  }
  const rel = app.relation(userId);
  const name = app.user(userId)?.displayName ?? t("common.unknownUser");
  // New DMs only with friends; an existing DM stays reachable (history).
  const hasDM = Object.values(app.channels).some((c) => c.kind === "dm" && c.memberIds?.includes(userId));
  if (rel === "friend" || (rel !== "blocked" && hasDM)) {
    items.push({ label: t("user.message"), icon: MessageSquare, action: () => run(app.openDM([userId])) });
  }
  if (opts.onMention) items.push({ label: t("user.mention"), icon: AtSign, action: opts.onMention });
  items.push({ separator: true });
  if (rel === "friend") {
    items.push({
      label: t("user.removeFriend"),
      icon: UserMinus,
      action: () => {
        if (confirm(t("friends.removeConfirm", { name }))) run(app.removeRelation(userId));
      },
    });
  } else if (rel === "pending_in") {
    items.push({
      label: t("friends.accept"),
      icon: UserPlus,
      action: () => run(app.setRelation(userId, "friend")),
    });
  } else if (rel === "pending_out") {
    items.push({
      label: t("friends.cancelRequest"),
      icon: UserMinus,
      action: () => run(app.removeRelation(userId)),
    });
  } else if (rel !== "blocked") {
    items.push({
      label: t("user.addFriend"),
      icon: UserPlus,
      action: () => run(app.setRelation(userId, "friend"), t("friends.requestSent")),
    });
  }
  if (rel === "blocked") {
    items.push({ label: t("user.unblock"), icon: Ban, action: () => run(app.removeRelation(userId)) });
  } else {
    items.push({
      label: t("user.block"),
      icon: Ban,
      danger: true,
      action: () => {
        if (confirm(t("user.blockConfirm", { name }))) run(app.setRelation(userId, "blocked"));
      },
    });
  }
  items.push({ separator: true });
  items.push({
    label: t("user.copyId"),
    icon: Copy,
    action: () => void navigator.clipboard.writeText(userId).then(() => toast(t("toast.copied"), "success")),
  });
  return items;
}
