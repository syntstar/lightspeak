// Desktop integration: tray menu/state, tray actions, deep links, automatic
// "away" after 10 minutes without input, network change → reconnect.
import { listen } from "@tauri-apps/api/event";
import type { Status } from "./api/types";
import { t } from "./i18n/index.svelte";
import { native, inTauri, type TrayState } from "./native";
import { prefs } from "./prefs.svelte";
import { app } from "./state/app.svelte";
import { local } from "./state/local.svelte";
import { trayVoiceRows } from "./trayVoice";
import { voice } from "./voice/voice.svelte";

export const IDLE_AFTER_S = 600;

export function initDesktop(onInvite: (code: string) => void): void {
  window.addEventListener("online", () => app.nudge());
  window.addEventListener("focus", () => (app.focused = true));
  window.addEventListener("blur", () => (app.focused = false));
  if (!inTauri) return;

  void native.setCloseToTray(prefs.closeToTray);
  void native.setMemorySaver(prefs.memorySaver);

  // Tray menu + icon state follow language, status, unread, mute and who is
  // in voice. Rebuilt only when the resulting state actually changes.
  let lastTray = "";
  let lastState: TrayState | null = null;
  $effect.root(() => {
    $effect(() => {
      const status = app.me?.status ?? "online";
      const unread = app.anyUnread;
      const voiceRows = trayVoiceRows(
        Object.values(app.voiceStates),
        (id) => {
          const c = app.channels[id];
          const s = c?.spaceId ? app.spaces[c.spaceId] : undefined;
          return c && s ? { name: c.name, space: s.name } : null;
        },
        (id) => app.user(id)?.displayName ?? t("common.unknownUser"),
        voice.channelId,
      );
      const state: TrayState = {
        labels: {
          open: t("tray.open"),
          mute: t("tray.mute"),
          deafen: t("tray.deafen"),
          status: t("tray.status"),
          online: t("status.online"),
          idle: t("status.idle"),
          dnd: t("status.dnd"),
          invisible: t("status.invisible"),
          quit: t("tray.quit"),
          inVoice: t("tray.inVoice"),
        },
        tooltip: voice.inCall ? t("tray.inCall") : unread ? t("tray.unread") : t("app.name"),
        muted: local.muted,
        deafened: local.deafened,
        status,
        unread,
        inCall: voice.inCall,
        voice: voiceRows,
      };
      const key = JSON.stringify(state);
      if (key === lastTray) return;
      lastTray = key;
      lastState = state;
      void native.trayUpdate(state).catch(() => (lastTray = ""));
    });
  });

  void listen<string>("tray", (e) => {
    const id = e.payload;
    // Windows flips a check item on click even when nothing changes (own
    // channel, current status): restore the real state first; an actual
    // change follows through the effect above.
    if (lastState) void native.trayUpdate(lastState).catch(() => {});
    if (id === "mute") voice.toggleMute();
    else if (id === "deafen") voice.toggleDeafen();
    else if (id.startsWith("voice:")) {
      const ch = id.slice(6);
      if (app.channels[ch] && voice.channelId !== ch) void voice.join(ch);
    } else if (id.startsWith("status:")) {
      local.autoIdle = false;
      app.setStatus(id.slice(7) as Exclude<Status, "offline">);
    }
  });

  void listen<string>("deep-link", (e) => onInvite(e.payload));

  // Auto-away: poll the OS idle time (cheap syscall) every 30 s.
  setInterval(() => {
    if (!app.me || app.phase !== "app") return;
    void native.idleSeconds().then((idle) => {
      if (!app.me) return;
      if (idle >= IDLE_AFTER_S && app.me.status === "online") {
        local.autoIdle = true;
        app.setStatus("idle");
      } else if (idle < IDLE_AFTER_S && local.autoIdle) {
        local.autoIdle = false;
        if (app.me.status === "idle") app.setStatus("online");
      }
    });
  }, 30_000);
}
