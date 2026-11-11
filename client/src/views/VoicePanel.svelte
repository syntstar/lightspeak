<script lang="ts">
  // Sidebar block above the user panel while in a call: connection state,
  // channel, encryption lock, ping and disconnect.
  import Lock from "@lucide/svelte/icons/lock";
  import PhoneOff from "@lucide/svelte/icons/phone-off";
  import ShieldAlert from "@lucide/svelte/icons/shield-alert";
  import MicOff from "@lucide/svelte/icons/mic-off";
  import Headphones from "@lucide/svelte/icons/headphones";
  import Moon from "@lucide/svelte/icons/moon";
  import ScreenShare from "@lucide/svelte/icons/screen-share";
  import Repeat from "@lucide/svelte/icons/repeat";
  import ScreenShareOff from "@lucide/svelte/icons/screen-share-off";
  import Settings2 from "@lucide/svelte/icons/settings-2";
  import Eye from "@lucide/svelte/icons/eye";
  import VolumeX from "@lucide/svelte/icons/volume-x";
  import MonitorPlay from "@lucide/svelte/icons/monitor-play";
  import { inTauri } from "../lib/native";
  import { openMenuAt, type MenuItem } from "../lib/ui/menu.svelte";
  import { audioSessions } from "../lib/voice/streamAudio";
  import { app } from "../lib/state/app.svelte";
  import { t } from "../lib/i18n/index.svelte";
  import { voice } from "../lib/voice/voice.svelte";
  import IconButton from "../lib/ui/IconButton.svelte";
  import Spinner from "../lib/ui/Spinner.svelte";

  const ch = $derived(voice.channelId ? app.channels[voice.channelId] : undefined);
  const where = $derived.by(() => {
    if (!ch) return "";
    if (ch.spaceId) return `${ch.name} · ${app.spaces[ch.spaceId]?.name ?? ""}`;
    return app.channelTitle(ch);
  });
  const myQuality = $derived(app.me ? voice.quality[app.me.id] : undefined);
  const level = $derived(
    voice.status !== "connected"
      ? "wait"
      : myQuality === "poor" || myQuality === "lost" || (voice.ping ?? 0) > 250
        ? "bad"
        : (voice.ping ?? 0) > 120
          ? "ok"
          : "good",
  );
  const statusText = $derived(
    voice.status === "connecting"
      ? t("voice.connecting")
      : voice.status === "waiting_key"
        ? t("voice.waitingKey")
        : voice.status === "reconnecting"
          ? t("voice.reconnecting")
          : t("voice.connected"),
  );

  const stream = voice.stream;
  const inAway = $derived(!!voice.channelId && app.channels[voice.channelId]?.mode === "away");
  const viewers = $derived(stream.viewers);
  const viewerNames = $derived(viewers.map((id) => app.user(id)?.displayName ?? "?").join(", "));
  const soundFrom = $derived.by(() => {
    const a = stream.own?.audio;
    if (!a) return t("stream.noSound");
    if (a.kind === "system") return t("stream.soundSystem");
    return t("stream.soundFrom", { app: a.processName ?? "?" });
  });

  /** "Audio from: …" picker: apps currently playing sound, whole system, default. */
  async function pickSound(e: MouseEvent) {
    const el = e.currentTarget as HTMLElement;
    const list = await audioSessions().catch(() => []);
    const a = stream.own?.audio;
    const items: MenuItem[] = [{ heading: t("stream.soundPick") }];
    for (const s of list.sort((x, y) => y.peak - x.peak)) {
      items.push({
        label: s.processName ?? `PID ${s.pid}`,
        checked: a?.kind === "pick" && a.processName === s.processName,
        action: () => void stream.pickAudio(s.pid),
      });
    }
    if (!list.length) items.push({ label: t("stream.soundNone"), disabled: true, action: () => {} });
    items.push({ separator: true });
    if (stream.own?.surface === "window")
      items.push({
        label: t("stream.soundDefault"),
        checked: a?.kind === "app",
        action: () => void stream.pickAudio("default"),
      });
    items.push({
      label: t("stream.soundSystem"),
      checked: a?.kind === "system",
      action: () => void stream.pickAudio("system"),
    });
    openMenuAt(el, items);
  }

  function open() {
    if (ch && !ch.spaceId) app.selectChannel(ch.id);
    else if (ch?.spaceId && app.spaceId !== ch.spaceId) app.selectSpace(ch.spaceId);
  }
  // What is being shared, short enough for the narrow panel (app name, else window title).
  const liveSource = $derived.by(() => {
    const c = stream.current;
    if (!c) return "";
    if (c.surface === "monitor") return t("stream.screenShort");
    return c.app ?? c.title ?? t("stream.window");
  });
</script>

{#if voice.inCall && ch}
  <div class="vp" role="region" aria-label={t("voice.panel")}>
    <div class="row">
      <div class="info">
        <span class="status {level}" aria-live="polite">
          {#if voice.status !== "connected"}<Spinner size={11} />{/if}
          {statusText}
          {#if voice.status === "connected" && voice.ping !== null}
            <span class="ping" title={t("voice.pingHint")}>{voice.ping} ms</span>
          {/if}
        </span>
        <button type="button" class="where" onclick={open} title={where}>
          {#if voice.encrypted}<span
              class="lock"
              title={t("voice.encrypted")}
              aria-label={t("voice.encrypted")}><Lock size={11} strokeWidth={2.25} /></span
            >{/if}
          <span class="nm">{where}</span>
        </button>
      </div>
      <IconButton label={t("voice.disconnect")} danger onclick={() => void voice.leave()} tip="top">
        <PhoneOff size={18} strokeWidth={1.75} />
      </IconButton>
    </div>
    {#if voice.playbackBlocked}
      <button type="button" class="hint warn unlock" onclick={() => void voice.resumeAudio()}
        ><VolumeX size={12} strokeWidth={2} />{t("voice.playbackBlocked")}</button
      >
    {:else if voice.healing}
      <p class="hint warn" role="status"><ShieldAlert size={12} strokeWidth={2} />{t("voice.healing")}</p>
    {/if}
    {#if voice.serverMuted}
      <p class="hint"><MicOff size={12} strokeWidth={2} />{t("voice.serverMutedHint")}</p>
    {:else if voice.listener}
      <p class="hint">
        {#if inAway}<Moon size={12} strokeWidth={2} />{t("rules.listenerAway")}{:else}<Headphones
            size={12}
            strokeWidth={2}
          />{t("rules.listener")}{/if}
      </p>
    {:else if voice.listenOnly}
      <p class="hint"><MicOff size={12} strokeWidth={2} />{t("voice.listenOnly")}</p>
    {/if}
    {#if voice.status === "connected" || stream.own}
      <div class="stream">
        {#if stream.own}
          <button
            type="button"
            class="live-info"
            onclick={() => void stream.watch(app.me!.id)}
            title={viewers.length ? viewerNames : t("stream.preview")}
          >
            <span class="live">LIVE</span>
            <span class="sr-only">{t("stream.youStream")}</span>
            <span class="nm" title={liveSource}>{liveSource}</span>
            <span class="eye" aria-label={t("stream.viewers", { count: viewers.length })}
              ><Eye size={12} strokeWidth={2} />{viewers.length}</span
            >
          </button>
          <IconButton
            label={t("stream.switchSource")}
            disabled={stream.busy}
            onclick={() => void stream.pick()}
            tip="top"
          >
            <Repeat size={16} strokeWidth={1.75} />
          </IconButton>
          <IconButton label={t("stream.settings")} onclick={() => (stream.dialog = true)} tip="top">
            <Settings2 size={16} strokeWidth={1.75} />
          </IconButton>
          <IconButton label={t("stream.stop")} danger onclick={() => void stream.stop()} tip="top">
            <ScreenShareOff size={16} strokeWidth={1.75} />
          </IconButton>
        {:else}
          <button
            type="button"
            class="share"
            disabled={stream.busy || voice.noScreen}
            title={voice.noScreen ? t("rules.noScreen") : undefined}
            onclick={() => stream.share()}
          >
            <ScreenShare size={15} strokeWidth={1.75} />{t("stream.share")}
          </button>
          {#if stream.watching.length && !stream.view}
            <IconButton label={t("stream.backToStream")} onclick={() => stream.open()} tip="top">
              <MonitorPlay size={16} strokeWidth={1.75} />
            </IconButton>
          {/if}
        {/if}
      </div>
      {#if stream.own && inTauri && stream.own.cfg.audio}
        <button type="button" class="sound" class:warn={stream.own.silent} onclick={pickSound}>
          {#if stream.own.silent || !stream.own.audio}<VolumeX size={12} strokeWidth={2} />{/if}
          <span class="nm"
            >{stream.own.silent
              ? t("stream.silentHint", { app: stream.own.audio?.processName ?? "?" })
              : soundFrom}</span
          >
        </button>
      {/if}
    {/if}
    {#if voice.elevatedForeground}
      <p class="hint warn"><ShieldAlert size={12} strokeWidth={2} />{t("voice.elevatedHint")}</p>
    {/if}
  </div>
{/if}

<style>
  .vp {
    padding: 8px 8px 6px 12px;
    border-top: 1px solid var(--separator);
    flex: none;
  }
  .row {
    display: flex;
    align-items: center;
    gap: 6px;
  }
  .info {
    flex: 1;
    min-width: 0;
    display: flex;
    flex-direction: column;
    line-height: 1.25;
  }
  .status {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    font-weight: 600;
    font-size: var(--fs-callout);
  }
  .status.good {
    color: var(--success-text);
  }
  .status.ok {
    color: var(--warning);
  }
  .status.bad {
    color: var(--danger);
  }
  .status.wait {
    color: var(--text-2);
  }
  .ping {
    font-weight: 400;
    color: var(--text-3);
    font-variant-numeric: tabular-nums;
  }
  .where {
    display: flex;
    align-items: center;
    gap: 4px;
    min-width: 0;
    padding: 0;
    border: none;
    background: none;
    color: var(--text-2);
    font-size: var(--fs-caption);
    text-align: left;
  }
  .where:hover .nm {
    text-decoration: underline;
  }
  .lock {
    display: inline-flex;
    color: var(--success-text);
  }
  .nm {
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .hint {
    display: flex;
    align-items: flex-start;
    gap: 5px;
    margin: 4px 0 0;
    font-size: var(--fs-caption);
    color: var(--text-2);
  }
  .hint :global(svg) {
    flex: none;
    margin-top: 2px;
  }
  .hint.warn {
    color: var(--warning);
  }
  button.unlock {
    width: 100%;
    padding: 0;
    border: none;
    background: none;
    font: inherit;
    font-size: var(--fs-caption);
    text-align: left;
    text-decoration: underline;
  }
  .stream {
    display: flex;
    align-items: center;
    gap: 4px;
    margin-top: 6px;
  }
  .share {
    flex: 1;
    display: inline-flex;
    align-items: center;
    justify-content: center;
    gap: 6px;
    height: 28px;
    border: none;
    border-radius: var(--r-sm);
    background: var(--bg-hover);
    color: var(--text-1);
    font-size: var(--fs-callout);
    font-weight: 500;
  }
  .share:hover:not(:disabled) {
    background: var(--bg-selected);
  }
  .share:disabled {
    color: var(--text-3);
    cursor: default;
  }
  .live-info {
    flex: 1;
    min-width: 0;
    display: flex;
    align-items: center;
    gap: 6px;
    height: 28px;
    padding: 0 6px;
    border: none;
    border-radius: var(--r-sm);
    background: transparent;
    color: var(--text-1);
    font-size: var(--fs-callout);
    font-weight: 500;
    text-align: left;
  }
  .live-info:hover {
    background: var(--bg-hover);
  }
  .live {
    flex: none;
    padding: 0 5px;
    border-radius: var(--r-xs);
    background: var(--live);
    color: #fff;
    font-size: 9.5px;
    font-weight: 700;
    letter-spacing: 0.04em;
  }
  .eye {
    display: inline-flex;
    align-items: center;
    gap: 3px;
    margin-left: auto;
    color: var(--text-3);
    font-size: var(--fs-caption);
    font-variant-numeric: tabular-nums;
  }
  .sound {
    display: flex;
    align-items: center;
    gap: 5px;
    max-width: 100%;
    margin: 2px 0 0;
    padding: 0 6px;
    border: none;
    background: none;
    color: var(--text-3);
    font-size: var(--fs-caption);
    text-align: left;
  }
  .sound:hover .nm {
    text-decoration: underline;
  }
  .sound.warn {
    color: var(--warning);
  }
</style>
