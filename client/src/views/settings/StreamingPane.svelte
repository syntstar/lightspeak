<script lang="ts">
  // Settings → Streaming: defaults for the share dialog (same values the
  // dialog remembers), codec and the encoder WebRTC picked last time.
  import { prefs, savePrefs, type StreamCodec } from "../../lib/prefs.svelte";
  import { formatDateTime, i18n, t } from "../../lib/i18n/index.svelte";
  import {
    bitrateLabel,
    maxBitrate,
    type StreamContent,
    type StreamFps,
    type StreamResolution,
    type StreamSurface,
  } from "../../lib/voice/streamPresets";
  import SegmentedControl from "../../lib/ui/SegmentedControl.svelte";
  import Toggle from "../../lib/ui/Toggle.svelte";

  let fps = $state(String(prefs.stream.fps));

  const enc = $derived(prefs.streamEncoder);
  const upload = $derived(bitrateLabel(maxBitrate(prefs.stream), i18n.locale));

  const surfaceOptions = $derived<{ value: StreamSurface; label: string }[]>([
    { value: "window", label: t("stream.window") },
    { value: "monitor", label: t("stream.screen") },
  ]);
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
  const codecOptions = $derived<{ value: StreamCodec; label: string }[]>([
    { value: "auto", label: t("stream.contentAuto") },
    { value: "h264", label: "H.264" },
    { value: "vp8", label: "VP8" },
  ]);

  function changed() {
    savePrefs();
  }
</script>

<h3 class="gh">{t("streaming.defaults")}</h3>
<div class="group">
  <div class="row">
    <span>{t("stream.what")}</span>
    <SegmentedControl
      label={t("stream.what")}
      bind:value={prefs.stream.surface}
      options={surfaceOptions}
      onchange={changed}
    />
  </div>
  <div class="row">
    <span>{t("stream.quality")}</span>
    <SegmentedControl
      label={t("stream.quality")}
      bind:value={prefs.stream.resolution}
      options={resOptions}
      onchange={changed}
    />
  </div>
  <div class="row">
    <span>{t("stream.fps")}</span>
    <SegmentedControl
      label={t("stream.fps")}
      bind:value={fps}
      options={fpsOptions}
      onchange={(v) => {
        prefs.stream.fps = Number(v) as StreamFps;
        changed();
      }}
    />
  </div>
  <div class="row">
    <span>{t("stream.content")}</span>
    <SegmentedControl
      label={t("stream.content")}
      bind:value={prefs.stream.content}
      options={contentOptions}
      onchange={changed}
    />
  </div>
  <div class="row">
    <span class="two">
      {t("stream.sound")}
      <small>{prefs.stream.surface === "window" ? t("stream.soundWindow") : t("stream.soundScreen")}</small>
    </span>
    <Toggle label={t("stream.sound")} bind:checked={prefs.stream.audio} onchange={changed} />
  </div>
</div>
<p class="note">{t("streaming.defaultsNote", { rate: upload })}</p>

<h3 class="gh">{t("streaming.encoding")}</h3>
<div class="group">
  <div class="row">
    <span class="two">
      {t("streaming.codec")}
      <small>{t("streaming.codecHint")}</small>
    </span>
    <SegmentedControl
      label={t("streaming.codec")}
      bind:value={prefs.streamCodec}
      options={codecOptions}
      onchange={changed}
    />
  </div>
  <div class="row">
    <span class="two">
      {t("streaming.hw")}
      <small>
        {#if enc}
          {t("streaming.lastStream", { date: formatDateTime(enc.at) })} · {enc.codec || "?"} · {enc.impl}
        {:else}
          {t("streaming.hwUnknownHint")}
        {/if}
      </small>
    </span>
    <span class="status" class:ok={enc?.hw === true} class:warn={enc?.hw === false}>
      {#if !enc || enc.hw === null}
        {t("streaming.hwUnknown")}
      {:else if enc.hw}
        {t("streaming.hwOn")}
      {:else}
        {t("streaming.hwOff")}
      {/if}
    </span>
  </div>
</div>

<style>
  .status {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    color: var(--text-2);
    white-space: nowrap;
  }
  .status::before {
    content: "";
    width: 8px;
    height: 8px;
    border-radius: 50%;
    background: var(--status-offline);
  }
  .status.ok::before {
    background: var(--success);
  }
  .status.warn::before {
    background: var(--warning);
  }
</style>
