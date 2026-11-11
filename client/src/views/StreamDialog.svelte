<script lang="ts">
  // Screen share dialog: the picked window/screen as a live
  // preview, quality, frame rate, content and sound. WebView2 only lets the
  // user pick a source in its own picker, so the dialog opens that
  // picker and shows the result here; nothing is sent before "Go live".
  // While streaming it changes the running stream; "Change" swaps the source
  // without interrupting it (7.7).
  import Monitor from "@lucide/svelte/icons/monitor";
  import AppWindow from "@lucide/svelte/icons/app-window";
  import ScreenShare from "@lucide/svelte/icons/screen-share";
  import { prefs } from "../lib/prefs.svelte";
  import { i18n, t } from "../lib/i18n/index.svelte";
  import { inTauri, isWeb } from "../lib/native";
  import { voice } from "../lib/voice/voice.svelte";
  import type { CaptureSource } from "../lib/voice/stream.svelte";
  import {
    bitrateLabel,
    maxBitrate,
    type StreamContent,
    type StreamFps,
    type StreamPrefs,
    type StreamResolution,
  } from "../lib/voice/streamPresets";
  import Sheet from "../lib/ui/Sheet.svelte";
  import Button from "../lib/ui/Button.svelte";
  import SegmentedControl from "../lib/ui/SegmentedControl.svelte";
  import Spinner from "../lib/ui/Spinner.svelte";
  import Toggle from "../lib/ui/Toggle.svelte";
  import { untrack } from "svelte";

  const stream = voice.stream;
  let p = $state<StreamPrefs>({ ...prefs.stream });
  let fps = $state<string>(String(prefs.stream.fps));

  // Re-seed the form each time the dialog opens. Only `stream.dialog` may be a
  // dependency: reading `p` back after assigning it made the effect depend on
  // itself, so it re-ran forever and reset every click.
  $effect(() => {
    if (!stream.dialog) return;
    const init = untrack(() => ({ ...(stream.own?.cfg ?? prefs.stream) }));
    p = init;
    fps = String(init.fps);
  });

  const live = $derived(!!stream.own);
  const src = $derived<CaptureSource | null>(live ? stream.current : stream.pending);
  const choice = $derived<StreamPrefs>({ ...p, fps: Number(fps) as StreamFps });
  const upload = $derived(bitrateLabel(maxBitrate(choice), i18n.locale));
  const surface = $derived(src?.surface ?? prefs.stream.surface);
  const srcName = $derived(
    !src ? "" : src.surface === "monitor" ? t("stream.screen") : (src.title ?? src.app ?? t("stream.window")),
  );

  const resOptions = $derived<{ value: StreamResolution; label: string }[]>([
    { value: "720", label: "720p" },
    { value: "1080", label: "1080p" },
    { value: "1440", label: "1440p" },
    { value: "source", label: t("stream.source") },
  ]);
  const fpsOptions = [
    { value: "15", label: "15" },
    { value: "30", label: "30" },
    { value: "60", label: "60" },
  ];
  const contentOptions = $derived<{ value: StreamContent; label: string }[]>([
    { value: "auto", label: t("stream.contentAuto") },
    { value: "motion", label: t("stream.contentMotion") },
    { value: "detail", label: t("stream.contentDetail") },
  ]);

  /** Shows a capture track in a <video> (muted, no extra decode). */
  function preview(node: HTMLVideoElement, track: MediaStreamTrack) {
    node.srcObject = new MediaStream([track]);
    return {
      update(next: MediaStreamTrack) {
        node.srcObject = new MediaStream([next]);
      },
      destroy() {
        node.srcObject = null;
      },
    };
  }

  async function go() {
    // goLive takes the preview synchronously, so closing right after is safe.
    const started = stream.goLive(choice);
    stream.dialog = false;
    await started;
  }

  async function apply() {
    stream.dialog = false;
    await stream.update(choice);
  }

  function cancel() {
    stream.dialog = false;
    if (!live) stream.cancelPending();
  }
</script>

<Sheet
  bind:open={stream.dialog}
  title={live ? t("stream.settings") : t("stream.share")}
  width={480}
  onclose={() => {
    if (!live) stream.cancelPending();
  }}
>
  {#if src}
    <div class="source">
      <video
        use:preview={src.track}
        muted
        autoplay
        playsinline
        aria-label={t("stream.previewOf", { name: srcName })}
      ></video>
      <div class="cap">
        <span class="ic" aria-hidden="true">
          {#if src.surface === "monitor"}<Monitor size={16} strokeWidth={1.75} />{:else}<AppWindow
              size={16}
              strokeWidth={1.75}
            />{/if}
        </span>
        <span class="names">
          <span class="nm" title={srcName}>{srcName}</span>
          {#if src.app && src.title && src.app.toLowerCase() !== src.title.toLowerCase()}<small
              >{src.app}</small
            >{/if}
        </span>
        <Button busy={stream.busy} onclick={() => void stream.pick()}>{t("stream.change")}</Button>
      </div>
    </div>
    {#if src.surface === "monitor"}<p class="privacy">{t("stream.screenPrivacy")}</p>{/if}
    {#if live}<p class="note">{t("stream.switchHint")}</p>{/if}
  {:else}
    <button type="button" class="pick" disabled={stream.busy} onclick={() => void stream.pick()}>
      {#if stream.busy}<Spinner size={20} />{:else}<ScreenShare size={26} strokeWidth={1.5} />{/if}
      <span class="ttl">{t("stream.pickSource")}</span>
      <span class="sub">{t("stream.pickHint")}</span>
    </button>
  {/if}

  <div class="field">
    <span class="lbl">{t("stream.quality")}</span>
    <SegmentedControl bind:value={p.resolution} options={resOptions} label={t("stream.quality")} />
  </div>
  <div class="field">
    <span class="lbl">{t("stream.fps")}</span>
    <SegmentedControl bind:value={fps} options={fpsOptions} label={t("stream.fps")} />
  </div>
  <div class="field">
    <span class="lbl">{t("stream.content")}</span>
    <SegmentedControl bind:value={p.content} options={contentOptions} label={t("stream.content")} />
  </div>
  {#if inTauri}
    <div class="row">
      <div class="txt">
        <span>{t("stream.sound")}</span>
        <small>{surface === "window" ? t("stream.soundWindow") : t("stream.soundScreen")}</small>
      </div>
      <Toggle bind:checked={p.audio} label={t("stream.sound")} />
    </div>
  {:else if isWeb}
    <!-- Browser: the sound is whatever was shared in the browser's picker. -->
    <div class="row">
      <div class="txt">
        <span>{t("stream.sound")}</span>
        <small>{src?.audioTrack ? t("web.streamSound") : t("web.streamNoSound")}</small>
      </div>
      <Toggle bind:checked={p.audio} label={t("stream.sound")} disabled={!src?.audioTrack} />
    </div>
  {/if}
  <p class="note">{t("stream.upload", { rate: upload })}</p>

  {#snippet footer()}
    <Button onclick={cancel}>{t("common.cancel")}</Button>
    {#if live}
      <Button variant="primary" onclick={() => void apply()}>{t("stream.apply")}</Button>
    {:else}
      <Button variant="primary" disabled={!src || stream.busy} onclick={() => void go()}
        >{t("stream.start")}</Button
      >
    {/if}
  {/snippet}
</Sheet>

<style>
  .source {
    flex: none;
    border: 1px solid var(--separator);
    border-radius: var(--r-md);
    overflow: hidden;
    background: var(--bg-elevated, transparent);
  }
  video {
    display: block;
    width: 100%;
    /* Small enough that the whole dialog fits a 600 px high window. */
    height: clamp(110px, 26vh, 240px);
    object-fit: contain;
    background: #000;
  }
  .cap {
    display: flex;
    align-items: center;
    gap: 10px;
    padding: 8px 8px 8px 12px;
  }
  .ic {
    display: flex;
    color: var(--text-2);
  }
  .names {
    flex: 1;
    min-width: 0;
    display: flex;
    flex-direction: column;
    line-height: 1.25;
  }
  .nm {
    font-weight: 600;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .names small {
    color: var(--text-2);
    font-size: var(--fs-caption);
  }
  .pick {
    flex: none;
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    gap: 6px;
    width: 100%;
    height: clamp(110px, 26vh, 240px);
    padding: 16px;
    border: 1px dashed var(--separator-strong, var(--separator));
    border-radius: var(--r-md);
    background: var(--bg-elevated, transparent);
    color: var(--accent-text);
    text-align: center;
  }
  .pick:hover:not(:disabled) {
    background: var(--bg-hover);
  }
  .pick .ttl {
    font-weight: 600;
    color: var(--text-1);
  }
  .pick .sub {
    max-width: 320px;
    font-size: var(--fs-caption);
    color: var(--text-2);
  }
  .field {
    display: flex;
    flex-direction: column;
    gap: 6px;
  }
  .lbl {
    font-size: var(--fs-callout);
    font-weight: 500;
  }
  .row {
    display: flex;
    align-items: center;
    gap: 12px;
  }
  .txt {
    flex: 1;
    display: flex;
    flex-direction: column;
  }
  .txt small {
    color: var(--text-2);
    font-size: var(--fs-caption);
  }
  .note {
    margin: 0;
    color: var(--text-3);
    font-size: var(--fs-caption);
  }
  .privacy {
    margin: 0;
    padding: 6px 10px;
    border-radius: var(--r-sm);
    background: color-mix(in srgb, var(--warning) 14%, transparent);
    font-size: var(--fs-caption);
    color: var(--text-1);
  }
</style>
