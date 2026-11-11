<script lang="ts">
  // Settings → Notifications: Windows toasts, sounds, taskbar badge and the
  // daily quiet hours. Per channel/space levels live in the
  // channel context menu.
  import { prefs, savePrefs } from "../../lib/prefs.svelte";
  import { t } from "../../lib/i18n/index.svelte";
  import { native, hasStore, isWeb } from "../../lib/native";
  import { doNotDisturb } from "../../lib/notifier.svelte";
  import Button from "../../lib/ui/Button.svelte";
  import Toggle from "../../lib/ui/Toggle.svelte";
  import { toast } from "../../lib/ui/toast.svelte";

  const toHHMM = (m: number) =>
    `${String(Math.floor(m / 60)).padStart(2, "0")}:${String(m % 60).padStart(2, "0")}`;
  const fromHHMM = (s: string): number | null => {
    const m = /^(\d{2}):(\d{2})$/.exec(s);
    if (!m) return null;
    const v = Number(m[1]) * 60 + Number(m[2]);
    return v >= 0 && v < 1440 ? v : null;
  };

  let tick = $state(0);
  // Re-evaluate "quiet right now" when the times change (and once a minute).
  $effect(() => {
    const id = setInterval(() => tick++, 60_000);
    return () => clearInterval(id);
  });
  const quietNow = $derived.by(() => {
    void tick;
    void prefs.quiet.enabled;
    void prefs.quiet.from;
    void prefs.quiet.to;
    return doNotDisturb();
  });

  function changed() {
    savePrefs();
  }

  function setTime(which: "from" | "to", e: Event) {
    const v = fromHHMM((e.currentTarget as HTMLInputElement).value);
    if (v === null) return;
    prefs.quiet[which] = v;
    changed();
  }

  // Browser client: page notifications need the user's permission first.
  let permission = $state<"default" | "granted" | "denied" | "unsupported">("default");
  async function refreshPermission() {
    if (isWeb) permission = await native.notifyPermission();
  }
  void refreshPermission();
  async function allow() {
    permission = await native.notifyRequest();
  }

  async function test() {
    if (!hasStore) return;
    if (isWeb && permission !== "granted") await allow();
    try {
      await native.notifyShow({
        title: t("app.name"),
        body: t("notifications.testBody"),
        tag: "test",
        args: "",
      });
    } catch {
      toast(t("toast.error"), "error");
    }
  }
</script>

<div class="group">
  <div class="row">
    <span class="two">
      {t("notifications.toasts")}
      <small>{t("notifications.toastsHint")}</small>
    </span>
    <Toggle label={t("notifications.toasts")} bind:checked={prefs.toasts} onchange={changed} />
  </div>
  <div class="row">
    <span class="two">
      {t("notifications.preview")}
      <small>{t("notifications.previewHint")}</small>
    </span>
    <Toggle
      label={t("notifications.preview")}
      bind:checked={prefs.toastPreview}
      disabled={!prefs.toasts}
      onchange={changed}
    />
  </div>
  <div class="row">
    <span class="two">
      {t("notifications.streamAlerts")}
      <small>{t("notifications.streamAlertsHint")}</small>
    </span>
    <Toggle label={t("notifications.streamAlerts")} bind:checked={prefs.streamAlerts} onchange={changed} />
  </div>
  <div class="row">
    <span>{t("notifications.test")}</span>
    <Button disabled={!prefs.toasts || !hasStore || permission === "denied"} onclick={test}
      >{t("notifications.testSend")}</Button
    >
  </div>
  {#if isWeb}
    <div class="row">
      <span class="two">
        {t("web.notifyPermission")}
        <small
          >{permission === "granted"
            ? t("web.notifyGranted")
            : permission === "denied"
              ? t("web.notifyDenied")
              : permission === "unsupported"
                ? t("web.notifyUnsupported")
                : t("web.notifyAsk")}</small
        >
      </span>
      {#if permission === "default"}
        <Button onclick={() => void allow()}>{t("web.notifyAllow")}</Button>
      {/if}
    </div>
  {/if}
</div>

<div class="group">
  <div class="row">
    <span class="two">
      {t("notifications.sounds")}
      <small>{t("notifications.soundsHint")}</small>
    </span>
    <Toggle label={t("notifications.sounds")} bind:checked={prefs.audio.sounds} onchange={changed} />
  </div>
  <div class="row">
    <span class="two">
      {t("notifications.badge")}
      <small>{t("notifications.badgeHint")}</small>
    </span>
    <Toggle label={t("notifications.badge")} bind:checked={prefs.taskbarBadge} onchange={changed} />
  </div>
</div>

<h3 class="gh">{t("notifications.quiet")}</h3>
<div class="group">
  <div class="row">
    <span class="two">
      {t("notifications.quietEnable")}
      <small>{t("notifications.quietHint")}</small>
    </span>
    <Toggle label={t("notifications.quietEnable")} bind:checked={prefs.quiet.enabled} onchange={changed} />
  </div>
  <div class="row">
    <span>{t("notifications.quietFrom")}</span>
    <input
      class="time"
      type="time"
      aria-label={t("notifications.quietFrom")}
      value={toHHMM(prefs.quiet.from)}
      disabled={!prefs.quiet.enabled}
      onchange={(e) => setTime("from", e)}
    />
  </div>
  <div class="row">
    <span>{t("notifications.quietTo")}</span>
    <input
      class="time"
      type="time"
      aria-label={t("notifications.quietTo")}
      value={toHHMM(prefs.quiet.to)}
      disabled={!prefs.quiet.enabled}
      onchange={(e) => setTime("to", e)}
    />
  </div>
</div>
<p class="note">
  {quietNow ? t("notifications.quietActive") : ""}
  {t("notifications.perChannel")}
</p>

<style>
  .time {
    height: 26px;
    padding: 0 8px;
    border: 1px solid var(--border-hairline);
    border-radius: var(--r-sm);
    background: var(--bg-input);
    color: var(--text-1);
    font: inherit;
    color-scheme: light dark;
  }
  .time:disabled {
    opacity: 0.5;
  }
</style>
