// Opens links in the default browser. Links to other domains than our own
// server ask for confirmation first.
import { openUrl } from "@tauri-apps/plugin-opener";
import { api } from "./state/app.svelte";
import { t } from "./i18n/index.svelte";
import { safeUrl } from "./markdown";
import { inTauri } from "./platform";

export function openLink(href: string): void {
  const url = safeUrl(href);
  if (!url) return;
  const own = !!api.base && new URL(url).origin === api.base;
  if (!own && !window.confirm(t("chat.openLinkConfirm", { url }))) return;
  if (inTauri) void openUrl(url).catch(() => {});
  else window.open(url, "_blank", "noopener,noreferrer");
}
