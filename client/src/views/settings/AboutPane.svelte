<script lang="ts">
  // Settings → About & diagnostics: versions, server, logs folder and the
  // diagnostics report (written to Downloads, without secrets).
  import { onMount } from "svelte";
  import { app, api } from "../../lib/state/app.svelte";
  import { prefs } from "../../lib/prefs.svelte";
  import { i18n, t } from "../../lib/i18n/index.svelte";
  import { native, inTauri, isWeb, type SystemInfo } from "../../lib/native";
  import { buildReport, hostOf } from "../../lib/diagnostics";
  import { voice } from "../../lib/voice/voice.svelte";
  import { updater } from "../../lib/updater.svelte";
  import Button from "../../lib/ui/Button.svelte";
  import Toggle from "../../lib/ui/Toggle.svelte";
  import { toast } from "../../lib/ui/toast.svelte";
  import { openLink } from "../../lib/links";

  /** Where the source of this build is published (AGPL-3.0). */
  const SOURCE_URL = "https://github.com/syntstar/lightspeak";

  let system = $state<SystemInfo | null>(null);
  let exporting = $state(false);
  let lastExport = $state<string | null>(null);

  onMount(() => {
    if (inTauri)
      void native
        .systemInfo()
        .then((s) => (system = s))
        .catch(() => {});
  });

  async function serverVersion(): Promise<{ reachable: boolean; version: string | null }> {
    try {
      const ctl = new AbortController();
      const timer = setTimeout(() => ctl.abort(), 4000);
      const r = await fetch(api.url("/version"), { signal: ctl.signal });
      clearTimeout(timer);
      if (!r.ok) return { reachable: true, version: null };
      const body = (await r.json().catch(() => null)) as { server?: string } | null;
      return { reachable: true, version: body?.server ?? null };
    } catch {
      return { reachable: false, version: null };
    }
  }

  async function exportReport() {
    if (!inTauri || exporting) return;
    exporting = true;
    try {
      const [sv, devices, stats] = await Promise.all([
        app.server ? serverVersion() : Promise.resolve(null),
        navigator.mediaDevices?.enumerateDevices().catch(() => []) ?? Promise.resolve([]),
        // Two samples so the CPU value is filled.
        native
          .processStats()
          .then(() => new Promise((r) => setTimeout(r, 1000)))
          .then(() => native.processStats())
          .catch(() => null),
      ]);
      const report = buildReport({
        createdAt: new Date(),
        app: { version: app.info.version, profile: app.info.profile, dev: app.info.dev },
        system,
        userAgent: navigator.userAgent,
        locale: i18n.locale,
        server: app.server
          ? { host: hostOf(app.server), reachable: sv?.reachable ?? null, version: sv?.version ?? null }
          : null,
        signedIn: !!app.me,
        gateway: app.conn,
        voice: {
          status: voice.status,
          encrypted: voice.encrypted,
          ping: voice.ping,
          streaming: !!voice.stream.own,
          watching: voice.stream.watching.length,
        },
        devices: devices
          .filter((d) => d.kind !== "videoinput")
          .map((d) => ({ kind: d.kind, label: d.label, isDefault: d.deviceId === "default" })),
        stats,
        prefs,
      });
      lastExport = await native.diagnosticsExport(report);
      toast(t("about.exported"), "success");
    } catch (e) {
      console.warn("diagnostics export", e);
      toast(t("about.exportFailed"), "error");
    } finally {
      exporting = false;
    }
  }

  const updateText = $derived.by(() => {
    switch (updater.status) {
      case "checking":
        return t("about.updateChecking");
      case "current":
        return t("about.updateCurrent");
      case "available":
        return `${t("about.updateAvailable", { version: updater.info?.version ?? "" })} ${t("about.updateRestartHint")}`;
      case "ready":
        return `${t("about.updateReady", { version: updater.info?.version ?? "" })} ${t("about.updateRestartHint")}`;
      case "installing":
        return updater.progress === null
          ? t("about.updateDownloading")
          : t("about.updateProgress", { percent: Math.round(updater.progress * 100) });
      case "error":
        return t("about.updateError");
      case "unavailable":
        return t("about.updateUnconfigured");
      default:
        return app.info.updater ? t("about.updateAuto") : t("about.updateUnconfigured");
    }
  });
</script>

<div class="group">
  <div class="row">
    <span>{t("app.name")}</span>
    <span class="muted selectable"
      >{t("settings.version", { version: app.info.version })}{app.info.dev ? " (dev)" : ""}</span
    >
  </div>
  {#if isWeb}
    <div class="row">
      <span class="two">
        {t("about.updates")}
        <small>{t("web.updates")}</small>
      </span>
    </div>
    <div class="row">
      <span>{t("web.browser")}</span><span class="muted selectable">{app.info.computerName}</span>
    </div>
  {:else}
    <div class="row">
      <span class="two">
        {t("about.updates")}
        <small aria-live="polite">{updateText}</small>
      </span>
      {#if updater.status === "available" || updater.status === "ready" || updater.status === "installing"}
        <Button
          variant="primary"
          busy={updater.status === "installing"}
          onclick={() => void updater.install()}
          >{updater.status === "ready" ? t("about.updateRestart") : t("about.updateInstall")}</Button
        >
      {:else}
        <Button
          busy={updater.status === "checking"}
          disabled={!inTauri || (updater.status === "unavailable" && updater.error === "not_configured")}
          onclick={() => void updater.check()}>{t("about.updateCheck")}</Button
        >
      {/if}
    </div>
  {/if}
  {#if app.info.updater}
    <div class="row">
      <span class="two">
        {t("about.autoUpdate")}
        <small>{t("about.autoUpdateHint")}</small>
      </span>
      <Toggle
        label={t("about.autoUpdate")}
        checked={prefs.autoUpdate}
        onchange={(v) => void updater.setAuto(v)}
      />
    </div>
  {/if}
  <div class="row">
    <span>{t("settings.server")}</span><span class="muted selectable">{api.base || "–"}</span>
  </div>
  {#if app.info.profile !== "default"}
    <div class="row">
      <span>{t("settings.profile")}</span><span class="muted">{app.info.profile}</span>
    </div>
  {/if}
  {#if !isWeb}
    <div class="row">
      <span>{t("about.windows")}</span><span class="muted selectable">{system?.windows || "–"}</span>
    </div>
    <div class="row">
      <span>{t("about.webview")}</span><span class="muted selectable">{system?.webview2 || "–"}</span>
    </div>
  {/if}
  <div class="row">
    <span class="two">
      {t("about.source")}
      <small>{t("about.sourceHint")}</small>
    </span>
    <Button onclick={() => openLink(SOURCE_URL)}>{t("about.sourceOpen")}</Button>
  </div>
</div>

{#if !isWeb}
  <h3 class="gh">{t("about.diagnostics")}</h3>
  <div class="group">
    <div class="row">
      <span class="two">
        {t("about.logs")}
        <small>{t("about.logsHint")}</small>
      </span>
      <Button
        disabled={!inTauri}
        onclick={() => void native.logsOpen().catch(() => toast(t("toast.error"), "error"))}
        >{t("about.logsOpen")}</Button
      >
    </div>
    <div class="row">
      <span class="two">
        {t("about.report")}
        <small>{lastExport ? t("about.savedTo", { path: lastExport }) : t("about.reportHint")}</small>
      </span>
      <Button busy={exporting} disabled={!inTauri} onclick={exportReport}>{t("about.export")}</Button>
    </div>
  </div>
{/if}
<p class="note">{t("about.privacy")}</p>
