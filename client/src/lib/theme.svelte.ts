// Applies appearance preferences to <html> (theme, accent, motion, mica,
// chat font size) and keeps the native window theme (Mica tint) in sync.
import { getCurrentWindow, Effect } from "@tauri-apps/api/window";
import { i18n } from "./i18n/index.svelte";
import { inTauri } from "./native";
import { prefs } from "./prefs.svelte";

let systemDark = $state(true);

export const theme = {
  /** Effective theme after resolving "system". */
  get resolved(): "light" | "dark" {
    return prefs.theme === "system" ? (systemDark ? "dark" : "light") : prefs.theme;
  },
};

export function initAppearance(): void {
  const mq = window.matchMedia("(prefers-color-scheme: dark)");
  systemDark = mq.matches;
  mq.addEventListener("change", (e) => (systemDark = e.matches));

  $effect.root(() => {
    $effect(() => {
      const root = document.documentElement;
      root.dataset.theme = theme.resolved;
      if (prefs.accent === "blue") delete root.dataset.accent;
      else root.dataset.accent = prefs.accent;
      root.dataset.motion = prefs.reduceMotion ? "reduced" : "full";
      root.dataset.mica = prefs.mica ? "on" : "off";
      root.style.setProperty("--fs-chat", `${prefs.chatFontSize}px`);
    });
    $effect(() => {
      i18n.set(prefs.language);
    });
    if (inTauri) {
      $effect(() => {
        const t = prefs.theme === "system" ? null : prefs.theme;
        void getCurrentWindow()
          .setTheme(t)
          .catch(() => {});
      });
      $effect(() => {
        const w = getCurrentWindow();
        void (prefs.mica ? w.setEffects({ effects: [Effect.Mica] }) : w.clearEffects()).catch(() => {});
      });
    }
  });
}
