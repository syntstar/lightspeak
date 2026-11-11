<script lang="ts">
  // Settings in the style of macOS System Settings: sidebar with coloured
  // icon tiles, grouped inset lists on the right.
  import CircleUser from "@lucide/svelte/icons/circle-user";
  import MonitorUp from "@lucide/svelte/icons/monitor-up";
  import Bell from "@lucide/svelte/icons/bell";
  import Gauge from "@lucide/svelte/icons/gauge";
  import Palette from "@lucide/svelte/icons/palette";
  import AppWindow from "@lucide/svelte/icons/app-window";
  import Info from "@lucide/svelte/icons/info";
  import AudioLines from "@lucide/svelte/icons/audio-lines";
  import Keyboard from "@lucide/svelte/icons/keyboard";
  import Hand from "@lucide/svelte/icons/hand";
  import Server from "@lucide/svelte/icons/server";
  import AudioPane from "./settings/AudioPane.svelte";
  import ShortcutsPane from "./settings/ShortcutsPane.svelte";
  import PrivacyPane from "./settings/PrivacyPane.svelte";
  import AccountPane from "./settings/AccountPane.svelte";
  import StreamingPane from "./settings/StreamingPane.svelte";
  import NotificationsPane from "./settings/NotificationsPane.svelte";
  import PerformancePane from "./settings/PerformancePane.svelte";
  import AboutPane from "./settings/AboutPane.svelte";
  import ServerPane from "./settings/ServerPane.svelte";
  import ChevronLeft from "@lucide/svelte/icons/chevron-left";
  import { onMount, type Component } from "svelte";
  import { app } from "../lib/state/app.svelte";
  import { ACCENTS, prefs, savePrefs, type Accent, type ThemePref } from "../lib/prefs.svelte";
  import { t, type LocalePref, type MessageKey } from "../lib/i18n/index.svelte";
  import { native, inTauri, isWeb } from "../lib/native";
  import IconButton from "../lib/ui/IconButton.svelte";
  import SegmentedControl from "../lib/ui/SegmentedControl.svelte";
  import Select from "../lib/ui/Select.svelte";
  import Slider from "../lib/ui/Slider.svelte";
  import Toggle from "../lib/ui/Toggle.svelte";
  import { toast } from "../lib/ui/toast.svelte";
  import CaptionButtons from "./CaptionButtons.svelte";

  type Pane =
    | "account"
    | "audio"
    | "streaming"
    | "notifications"
    | "shortcuts"
    | "appearance"
    | "privacy"
    | "performance"
    | "general"
    | "server"
    | "about";
  /** Desktop-only panes, hidden in the browser client. */
  const DESKTOP_ONLY: Pane[] = ["performance", "general"];
  const allPanes: {
    id: Pane;
    label: MessageKey;
    icon: Component<{ size?: number; strokeWidth?: number }>;
    color: string;
  }[] = [
    { id: "account", label: "settings.account", icon: CircleUser, color: "#8E8E93" },
    { id: "audio", label: "settings.audio", icon: AudioLines, color: "#FF375F" },
    { id: "streaming", label: "settings.streaming", icon: MonitorUp, color: "#BF5AF2" },
    { id: "notifications", label: "settings.notifications", icon: Bell, color: "#FF453A" },
    { id: "shortcuts", label: "settings.shortcuts", icon: Keyboard, color: "#8E8E93" },
    { id: "appearance", label: "settings.appearance", icon: Palette, color: "#0A84FF" },
    { id: "privacy", label: "settings.privacy", icon: Hand, color: "#0A84FF" },
    { id: "performance", label: "settings.performance", icon: Gauge, color: "#FF9F0A" },
    { id: "general", label: "settings.general", icon: AppWindow, color: "#30D158" },
    { id: "server", label: "settings.server", icon: Server, color: "#64D2FF" },
    { id: "about", label: "settings.about", icon: Info, color: "#8E8E93" },
  ];
  // The server pane is only for server admins.
  const panes = $derived(
    allPanes.filter(
      (p) => !(isWeb && DESKTOP_ONLY.includes(p.id)) && (p.id !== "server" || !!app.me?.isServerAdmin),
    ),
  );
  let pane = $state<Pane>("account");
  let autostart = $state(false);

  const accentColors: Record<Accent, string> = {
    blue: "#0A84FF",
    purple: "#BF5AF2",
    pink: "#FF375F",
    orange: "#FF9F0A",
    green: "#30D158",
    graphite: "#8E8E93",
  };

  onMount(() => {
    if (inTauri) void native.autostartGet().then((v) => (autostart = v));
  });

  function changed() {
    savePrefs();
  }

  async function setAutostart(v: boolean) {
    try {
      await native.autostartSet(v);
    } catch {
      autostart = !v;
      toast(t("toast.error"), "error");
    }
  }

  function setCloseToTray(v: boolean) {
    changed();
    if (inTauri) void native.setCloseToTray(v);
  }

  function close() {
    app.settingsOpen = false;
  }
</script>

<svelte:window
  onkeydown={(e) => {
    if (e.key === "Escape" && !document.querySelector("dialog[open]")) close();
  }}
/>

<div class="settings" role="dialog" aria-modal="true" aria-label={t("settings.title")}>
  <nav class="side">
    <!-- Back sits top-left, away from the window's caption buttons, so it
         can't be confused with closing the app. -->
    <div class="side-head" data-tauri-drag-region>
      <IconButton label={t("settings.back")} aria-keyshortcuts="Escape" onclick={close}>
        <ChevronLeft size={20} strokeWidth={2} />
      </IconButton>
      <h1 data-tauri-drag-region>{t("settings.title")}</h1>
    </div>
    {#each panes as p (p.id)}
      <button
        type="button"
        class="pane"
        class:sel={pane === p.id}
        aria-current={pane === p.id}
        onclick={() => (pane = p.id)}
      >
        <span class="tile" style:background={p.color}><p.icon size={14} strokeWidth={2} /></span>
        {t(p.label)}
      </button>
    {/each}
  </nav>
  <section class="main">
    <header data-tauri-drag-region>
      <h2 data-tauri-drag-region>{t(panes.find((p) => p.id === pane)!.label)}</h2>
      <span class="spacer" data-tauri-drag-region></span>
      <CaptionButtons />
    </header>
    <div class="content">
      {#if pane === "account"}
        <AccountPane />
      {:else if pane === "audio"}
        <AudioPane />
      {:else if pane === "streaming"}
        <StreamingPane />
      {:else if pane === "notifications"}
        <NotificationsPane />
      {:else if pane === "performance"}
        <PerformancePane />
      {:else if pane === "shortcuts"}
        <ShortcutsPane />
      {:else if pane === "privacy"}
        <PrivacyPane />
      {:else if pane === "server"}
        <ServerPane />
      {:else if pane === "appearance"}
        <div class="group">
          <div class="row">
            <span>{t("settings.theme")}</span>
            <SegmentedControl
              label={t("settings.theme")}
              bind:value={prefs.theme}
              onchange={changed}
              options={[
                { value: "system" as ThemePref, label: t("settings.themeSystem") },
                { value: "light" as ThemePref, label: t("settings.themeLight") },
                { value: "dark" as ThemePref, label: t("settings.themeDark") },
              ]}
            />
          </div>
          <div class="row">
            <span>{t("settings.accent")}</span>
            <div class="accents" role="radiogroup" aria-label={t("settings.accent")}>
              {#each ACCENTS as a (a)}
                <button
                  type="button"
                  role="radio"
                  aria-checked={prefs.accent === a}
                  aria-label={t(`settings.accent.${a}`)}
                  class="dot"
                  style:background={accentColors[a]}
                  onclick={() => {
                    prefs.accent = a;
                    changed();
                  }}
                ></button>
              {/each}
            </div>
          </div>
          <div class="row">
            <span>{t("settings.language")}</span>
            <Select
              label={t("settings.language")}
              bind:value={prefs.language}
              onchange={changed}
              options={[
                { value: "system" as LocalePref, label: t("lang.system") },
                { value: "de" as LocalePref, label: t("lang.de") },
                { value: "en" as LocalePref, label: t("lang.en") },
              ]}
            />
          </div>
        </div>
        <div class="group">
          <div class="row">
            <span>{t("settings.fontSize")}</span>
            <Slider
              label={t("settings.fontSize")}
              bind:value={prefs.chatFontSize}
              min={12}
              max={16}
              reset={14}
              format={(v) => `${v} px`}
              onchange={changed}
            />
          </div>
          <div class="row">
            <span>{t("settings.compact")}</span>
            <Toggle label={t("settings.compact")} bind:checked={prefs.compact} onchange={changed} />
          </div>
          {#if !isWeb}
            <div class="row">
              <span>{t("settings.mica")}</span>
              <Toggle label={t("settings.mica")} bind:checked={prefs.mica} onchange={changed} />
            </div>
          {/if}
          <div class="row">
            <span>{t("settings.reduceMotion")}</span>
            <Toggle label={t("settings.reduceMotion")} bind:checked={prefs.reduceMotion} onchange={changed} />
          </div>
        </div>
      {:else if pane === "general"}
        <div class="group">
          <div class="row">
            <span>{t("settings.closeToTray")}</span>
            <Toggle
              label={t("settings.closeToTray")}
              bind:checked={prefs.closeToTray}
              onchange={setCloseToTray}
            />
          </div>
          <div class="row">
            <span class="two">
              {t("settings.autostart")}
              <small>{t("settings.autostartHint")}</small>
            </span>
            <Toggle
              label={t("settings.autostart")}
              bind:checked={autostart}
              onchange={(v) => void setAutostart(v)}
            />
          </div>
        </div>
      {:else if pane === "about"}
        <AboutPane />
      {/if}
    </div>
  </section>
</div>

<style>
  .settings {
    position: fixed;
    inset: 0;
    z-index: 500;
    display: flex;
    background: var(--bg-content);
  }
  .side {
    width: 220px;
    flex: none;
    padding: 0 10px;
    background: var(--bg-sidebar-solid);
    border-right: 1px solid var(--separator);
  }
  /* Same height as the content header, so both titles share a baseline. */
  .side-head {
    height: var(--titlebar-h);
    display: flex;
    align-items: center;
    gap: 4px;
    margin-bottom: 8px;
  }
  h1 {
    margin: 0;
    font-size: var(--fs-title3);
    font-weight: 650;
  }
  .pane {
    display: flex;
    align-items: center;
    gap: 10px;
    width: 100%;
    height: 30px;
    padding: 0 8px;
    margin-bottom: 2px;
    border: none;
    border-radius: var(--r-sm);
    background: transparent;
    text-align: left;
  }
  .pane:hover {
    background: var(--bg-hover);
  }
  .pane.sel {
    background: var(--accent);
    color: var(--on-accent);
  }
  .tile {
    width: 20px;
    height: 20px;
    border-radius: 6px;
    display: inline-flex;
    align-items: center;
    justify-content: center;
    color: #fff;
    flex: none;
  }
  .main {
    flex: 1;
    min-width: 0;
    display: flex;
    flex-direction: column;
  }
  header {
    height: var(--titlebar-h);
    display: flex;
    align-items: center;
    padding-left: 24px;
    gap: 4px;
    flex: none;
  }
  h2 {
    margin: 0;
    font-size: var(--fs-title3);
    font-weight: 650;
  }
  .spacer {
    flex: 1;
    align-self: stretch;
  }
  .content {
    flex: 1;
    overflow-y: auto;
    padding: 12px 24px 32px;
    max-width: 720px;
  }
  .content :global(.group) {
    margin-bottom: 20px;
    border-radius: var(--r-lg);
    background: var(--bg-input);
    border: 1px solid var(--separator);
  }
  .content :global(.row) {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 16px;
    min-height: 44px;
    padding: 8px 14px;
  }
  .content :global(.row + .row) {
    border-top: 1px solid var(--separator);
  }
  .content :global(.two) {
    display: flex;
    flex-direction: column;
  }
  .content :global(small) {
    color: var(--text-2);
    font-size: var(--fs-caption);
  }
  .content :global(.muted) {
    color: var(--text-2);
  }
  .content :global(.gh) {
    margin: 0 0 6px 14px;
    font-size: var(--fs-caption);
    font-weight: 600;
    color: var(--text-2);
  }
  .content :global(.note) {
    margin: -12px 14px 20px;
    font-size: var(--fs-caption);
    color: var(--text-2);
  }
  .accents {
    display: flex;
    gap: 10px;
  }
  .dot {
    width: 20px;
    height: 20px;
    border-radius: 50%;
    border: none;
    padding: 0;
    box-shadow: inset 0 0 0 0.5px rgba(0, 0, 0, 0.2);
  }
  .dot[aria-checked="true"] {
    box-shadow:
      0 0 0 2px var(--bg-content),
      0 0 0 4px var(--text-3);
  }
</style>
