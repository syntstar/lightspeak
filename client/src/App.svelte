<script lang="ts">
  import { onMount } from "svelte";
  import { api, app } from "./lib/state/app.svelte";
  import { fetchServerInfo, parseInvite, serverHost, type ServerInfo } from "./lib/server";
  import { prefs } from "./lib/prefs.svelte";
  import { errorCode } from "./lib/api/client";
  import { isWeb } from "./lib/native";
  import Button from "./lib/ui/Button.svelte";
  import Sheet from "./lib/ui/Sheet.svelte";
  import { initDesktop } from "./lib/desktop.svelte";
  import { initNotifier } from "./lib/notifier.svelte";
  import { updater } from "./lib/updater.svelte";
  import { t, terr } from "./lib/i18n/index.svelte";
  import ContextMenuHost from "./lib/ui/ContextMenuHost.svelte";
  import ToastHost from "./lib/ui/ToastHost.svelte";
  import Auth from "./views/Auth.svelte";
  import Main from "./views/Main.svelte";
  import QuickSwitcher from "./views/QuickSwitcher.svelte";

  let joinCode = $state("");
  let joinOpen = $state(false);

  // Switching servers because of an invite link always asks first.
  let switchOpen = $state(false);
  let switchTo = $state<{
    origin: string;
    link: string;
    info: ServerInfo | null;
    error: string | null;
  } | null>(null);

  /** Invite link from a deep link, the join sheet or the startup arguments. */
  function onInvite(link: string) {
    if (app.phase !== "app") {
      app.pendingInvite = link; // the auth screen handles it
      return;
    }
    const inv = parseInvite(link);
    if (!inv) return;
    if (inv.server && inv.server !== app.server && isWeb) {
      // The browser client is bound to its own server: the other
      // server's invite page offers its own browser client.
      window.open(link, "_blank", "noopener,noreferrer");
      return;
    }
    if (inv.server && inv.server !== app.server) {
      const target = { origin: inv.server, link, info: null, error: null };
      switchTo = target;
      switchOpen = true;
      fetchServerInfo(inv.server)
        .then((info) => {
          if (switchTo?.origin === target.origin) switchTo = { ...target, info };
        })
        .catch((e: unknown) => {
          const code = errorCode(e);
          if (switchTo?.origin === target.origin)
            switchTo = { ...target, error: code === "network" ? t("server.unreachable") : terr(code) };
        });
      return;
    }
    joinCode = inv.code;
    joinOpen = true;
  }

  async function confirmSwitch() {
    const s = switchTo;
    if (!s?.info) return;
    switchOpen = false;
    switchTo = null;
    app.useServer(s.origin, s.info.name);
    await app.switchServer(s.origin);
    app.pendingInvite = s.link; // join there (app) or register (auth screen)
  }

  // Update checks start once signed in (they need the server's address).
  $effect(() => {
    if (app.phase === "app") updater.start();
  });

  // Pending invite once the app is ready (startup link, server switch).
  $effect(() => {
    if (app.phase === "app" && app.pendingInvite) {
      const link = app.pendingInvite;
      app.pendingInvite = null;
      onInvite(link);
    }
  });

  onMount(() => {
    // Dev builds only: expose state for the CDP test scripts.
    if (import.meta.env.DEV) Object.assign(window, { __lv: { app, api } });
    initDesktop(onInvite);
    initNotifier();
    void app.boot();
  });

  function keydown(e: KeyboardEvent) {
    if (app.phase !== "app") return;
    const mod = e.ctrlKey && !e.altKey && !e.metaKey;
    if (mod && !e.shiftKey && e.key.toLowerCase() === "k") {
      e.preventDefault();
      app.quickSwitcher = !app.quickSwitcher;
    } else if (mod && !e.shiftKey && e.key === ",") {
      e.preventDefault();
      app.settingsOpen = true;
    } else if (e.altKey && !e.ctrlKey && !e.shiftKey && (e.key === "ArrowUp" || e.key === "ArrowDown")) {
      e.preventDefault();
      app.moveChannel(e.key === "ArrowUp" ? -1 : 1);
    }
  }
</script>

<svelte:window onkeydown={keydown} />

{#if app.phase === "boot"}
  <div class="boot" data-tauri-drag-region aria-label={t("app.loading")}></div>
{:else if app.phase === "auth"}
  <Auth />
{:else}
  <Main bind:joinCode bind:joinOpen />
  <QuickSwitcher />
  {#if app.settingsOpen}
    {#await import("./views/Settings.svelte") then m}<m.default />{/await}
  {/if}
  {#if app.updateRequired}
    <div class="update" role="alert">
      {t("app.updateRequired")}
      {#if updater.status === "available" || updater.status === "ready" || updater.status === "installing"}
        <Button
          variant="primary"
          busy={updater.status === "installing"}
          onclick={() => void updater.install()}>{t("app.updateNow")}</Button
        >
      {/if}
    </div>
  {:else if updater.bannerVisible || updater.status === "installing"}
    <div class="update optional" role="status">
      <span
        >{updater.status === "ready"
          ? t("app.updateReady", { version: updater.info?.version ?? "" })
          : t("app.updateAvailable", { version: updater.info?.version ?? "" })}</span
      >
      <Button variant="primary" busy={updater.status === "installing"} onclick={() => void updater.install()}
        >{updater.status === "ready" ? t("app.updateRestart") : t("app.updateNow")}</Button
      >
      {#if updater.status !== "installing"}
        <Button variant="plain" onclick={() => (updater.dismissed = updater.info?.version ?? null)}
          >{t("app.updateLater")}</Button
        >
      {/if}
    </div>
  {/if}
{/if}
<Sheet bind:open={switchOpen} title={t("server.switchTitle")} onclose={() => (switchTo = null)}>
  {#if switchTo}
    <p class="switch-body">
      {t("server.switchBody", {
        host: serverHost(switchTo.origin),
        current: prefs.servers[app.server]?.name || serverHost(app.server),
      })}
    </p>
    {#if switchTo.info}
      <p class="switch-server"><strong>{switchTo.info.name}</strong> · {serverHost(switchTo.origin)}</p>
    {:else if switchTo.error}
      <p class="switch-error" role="alert">{switchTo.error}</p>
    {/if}
  {/if}
  {#snippet footer()}
    <Button onclick={() => (switchOpen = false)}>{t("common.cancel")}</Button>
    <Button variant="primary" disabled={!switchTo?.info} onclick={() => void confirmSwitch()}
      >{t("server.switchConfirm")}</Button
    >
  {/snippet}
</Sheet>
<ContextMenuHost />
<ToastHost />

<style>
  .switch-body,
  .switch-server,
  .switch-error {
    margin: 0;
  }
  .switch-body {
    color: var(--text-2);
  }
  .switch-error {
    color: var(--danger);
  }
  .boot {
    height: 100%;
    background: var(--bg-content);
  }
  .update {
    position: fixed;
    left: 50%;
    top: 52px;
    transform: translateX(-50%);
    z-index: 900;
    max-width: 520px;
    padding: 10px 16px;
    border-radius: var(--r-lg);
    background: var(--warning);
    color: #1c1c1e;
    box-shadow: var(--shadow-pop);
    font-weight: 500;
    display: flex;
    align-items: center;
    gap: 12px;
  }
  .update.optional {
    background: var(--bg-elevated);
    color: var(--text-1);
    border: 1px solid var(--border-hairline);
    font-weight: 400;
  }
</style>
