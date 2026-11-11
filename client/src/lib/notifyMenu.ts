// Context-menu entries for notification settings of a channel, DM or space.
import Bell from "@lucide/svelte/icons/bell";
import BellOff from "@lucide/svelte/icons/bell-off";
import type { ID } from "./api/types";
import { app } from "./state/app.svelte";
import { t } from "./i18n/index.svelte";
import type { NotifyLevel } from "./notifyRules";
import type { MenuItem } from "./ui/menu.svelte";

export function notifyMenuItems(target: { kind: "channel" | "space"; id: ID }): MenuItem[] {
  const own = app.notify[target.id];
  const c = target.kind === "channel" ? app.channels[target.id] : undefined;
  const level: NotifyLevel = c ? app.notifyLevel(c) : (own?.level ?? "mentions");
  const muted = !!own?.muted;
  const lv = (l: NotifyLevel, label: string): MenuItem => ({
    label,
    checked: level === l,
    action: () => app.setNotify(target.id, { level: l }),
  });
  const items: MenuItem[] = [
    { heading: t("notify.heading") },
    lv("all", t("notify.all")),
    lv("mentions", t("notify.mentions")),
    lv("none", t("notify.none")),
  ];
  if (c?.spaceId && own?.level) {
    items.push({ label: t("notify.inherit"), action: () => app.setNotify(target.id, { level: null }) });
  }
  items.push({ separator: true });
  items.push({
    label: muted
      ? target.kind === "space"
        ? t("notify.unmuteSpace")
        : t("notify.unmute")
      : target.kind === "space"
        ? t("notify.muteSpace")
        : t("notify.mute"),
    icon: muted ? Bell : BellOff,
    action: () => app.setNotify(target.id, { muted: !muted }),
  });
  return items;
}
