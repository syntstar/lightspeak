// Windows toasts for new messages, taskbar flash and overlay
// badge, toast clicks → open the channel, "X is streaming" for the own voice
// channel. Event-driven only: costs nothing while idle.
import { getCurrentWindow, UserAttentionType } from "@tauri-apps/api/window";
import type { ID, Message, VoiceState } from "./api/types";
import { parse, toPlain } from "./markdown";
import { native, inTauri, hasStore } from "./native";
import { inQuietHours, shouldToast } from "./notifyRules";
import { prefs } from "./prefs.svelte";
import { app } from "./state/app.svelte";
import { t } from "./i18n/index.svelte";
import { toast } from "./ui/toast.svelte";
import { voice } from "./voice/voice.svelte";

/** Handlers for toast/button arguments other than `open:` (calls, phase 4). */
const actionHandlers: ((args: string) => boolean)[] = [];
export function onToastAction(fn: (args: string) => boolean): void {
  actionHandlers.push(fn);
}

/** "Do not disturb": DND status or the scheduled quiet hours. */
export function doNotDisturb(now = new Date()): boolean {
  return app.me?.status === "dnd" || inQuietHours(prefs.quiet, now);
}

/** Sharing the whole screen: toasts would be visible in the stream (9.5). */
function sharingScreen(): boolean {
  return voice.stream.own?.surface === "monitor";
}

function toastFor(m: Message): { title: string; body: string } {
  const c = app.channels[m.channelId];
  const author = app.user(m.authorId)?.displayName ?? m.author?.displayName ?? t("common.unknownUser");
  let title = author;
  if (c?.kind === "group_dm") title = `${author} · ${app.channelTitle(c)}`;
  else if (c?.spaceId) title = `${author} · #${c.name} · ${app.spaces[c.spaceId]?.name ?? ""}`;
  let body: string;
  if (!prefs.toastPreview || sharingScreen()) body = t("notify.newMessage");
  else {
    const mention = (id: string) => app.user(id)?.displayName ?? "…";
    body = toPlain(parse(m.content), mention).trim();
    if (m.attachments?.length) {
      const files = t("notify.files", { names: m.attachments.map((a) => a.filename).join(", ") });
      body = body ? `${body}\n${files}` : files;
    }
  }
  return { title, body: body || t("notify.newMessage") };
}

/** Users currently streaming; only a change false → true raises an alert. */
const live = new Set<ID>();

function syncLive(): void {
  live.clear();
  for (const v of Object.values(app.voiceStates)) if (v.channelId && v.streaming) live.add(v.userId);
}

function streamStarted(v: VoiceState): void {
  const me = app.me;
  if (!me || v.userId === me.id || !prefs.streamAlerts || doNotDisturb()) return;
  if (!voice.channelId || v.channelId !== voice.channelId) return;
  if (app.relation(v.userId) === "blocked") return;
  const name = app.user(v.userId)?.displayName ?? t("common.unknownUser");
  const visible = app.focused && document.visibilityState === "visible";
  if (visible) {
    toast(t("stream.startedInApp", { name }));
    return;
  }
  if (!prefs.toasts) return;
  void native
    .notifyShow({
      title: t("stream.startedTitle", { name }),
      body: t("stream.startedBody"),
      tag: `live:${v.userId}`,
      args: `watch:${v.userId}`,
    })
    .catch(() => {});
}

export function initNotifier(): void {
  // Desktop: Windows toasts; browser: page notifications.
  if (!hasStore) return;

  app.on((op, d) => {
    if (op === "ready") syncLive();
    else if (op === "voice_state_update") {
      const v = d as VoiceState;
      const was = live.has(v.userId);
      const now = !!v.channelId && v.streaming;
      if (now) live.add(v.userId);
      else live.delete(v.userId);
      if (now && !was) streamStarted(v);
      // A stale "is live" toast would only lead to an ended stream.
      else if (was && !now) void native.notifyClear(`live:${v.userId}`).catch(() => {});
    } else if (op === "message_create") {
      const m = d as Message;
      const c = app.channels[m.channelId];
      const me = app.me;
      if (!c || !me) return;
      const visible = typeof document !== "undefined" && document.visibilityState === "visible";
      const mentionsMe = m.mentions?.includes(me.id) || m.mentionsEveryone;
      const ok = shouldToast(app.notify, {
        channel: c,
        authorId: m.authorId,
        meId: me.id,
        mentionsMe,
        myStatus: doNotDisturb() ? "dnd" : me.status,
        viewing: app.channelId === c.id && app.focused && visible,
        authorBlocked: app.relation(m.authorId) === "blocked",
        enabled: prefs.toasts,
      });
      if (!ok) return;
      const { title, body } = toastFor(m);
      void native.notifyShow({ title, body, tag: c.id, args: `open:${c.id}` }).catch(() => {});
      if (!app.focused && inTauri)
        void getCurrentWindow().requestUserAttention(UserAttentionType.Informational);
    } else if (op === "read_state_update") {
      // Read here or on another device: remove the channel's toasts.
      void native.notifyClear((d as { channelId: string }).channelId).catch(() => {});
    }
  });

  void native.onToast((a) => {
    if (a.startsWith("open:")) {
      const id = a.slice(5);
      if (app.channels[id]) app.selectChannel(id);
      return;
    }
    if (a.startsWith("watch:")) {
      void voice.stream.watch(a.slice(6));
      return;
    }
    for (const h of actionHandlers) if (h(a)) return;
  });

  // Taskbar overlay dot while mentions/unread DMs are waiting.
  $effect.root(() => {
    let last: boolean | null = null;
    $effect(() => {
      const on = app.anyUnread && prefs.taskbarBadge;
      if (on === last) return;
      last = on;
      void native.taskbarBadge(on, on ? t("tray.unread") : "").catch(() => {});
    });
  });
}
