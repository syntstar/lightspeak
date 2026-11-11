<script lang="ts">
  // One stream: the video element LiveKit attaches to. With adaptiveStream the
  // element's size and visibility decide which layer (or none) is received.
  import type { Track } from "livekit-client";
  import Check from "@lucide/svelte/icons/check";
  import type { ID } from "../../lib/api/types";
  import { app } from "../../lib/state/app.svelte";
  import { t } from "../../lib/i18n/index.svelte";
  import Spinner from "../../lib/ui/Spinner.svelte";

  let {
    userId,
    track,
    big = false,
    onselect,
    onfullscreen,
  }: {
    userId: ID;
    track: Track | undefined;
    big?: boolean;
    onselect?: () => void;
    onfullscreen?: () => void;
  } = $props();

  let el = $state<HTMLVideoElement>();
  const name = $derived(app.user(userId)?.displayName ?? t("common.unknownUser"));
  const self = $derived(userId === app.me?.id);

  $effect(() => {
    const tr = track;
    const v = el;
    if (!tr || !v) return;
    tr.attach(v);
    return () => {
      tr.detach(v);
    };
  });

  // ---- quality hint (large view of someone else's stream) ----
  // "Quality improving …" while the streamer's full layer ramps up or a
  // lower layer still arrives, then "Full quality · 1080p60" for a moment.
  // Only when the tile is large enough to get the full layer: in a small
  // tile the preview layer is right, and the paused full layer (dynacast)
  // reads as "ramping" on the streamer's side.
  const info = $derived(self ? undefined : app.voiceStates[userId]?.stream);
  const label = $derived(info ? `${info.height}p${info.fps}` : "");
  /** Decoded height and whether the element is large enough to get the full layer. */
  let shownH = $state(0);
  let large = $state(false);
  function measure() {
    const v = el;
    if (!v) return;
    shownH = v.videoHeight;
    large = v.clientHeight * devicePixelRatio > 400;
  }
  $effect(() => {
    if (!big || self || !el) return;
    measure();
    const iv = setInterval(measure, 1000);
    return () => clearInterval(iv);
  });
  // No decoded picture yet (shownH 0) counts as lower too: frames are on their way.
  const rising = $derived(!!info && !!track && large && (info.ramp || shownH < info.height * 0.9));
  // Debounced: a short keyframe gap must not flash the hint. Shown at most
  // RISE_MAX_MS per episode: a lasting limit (slow line, or a streamer whose
  // client keeps reporting a ramp) is no "improving".
  const RISE_MAX_MS = 25_000;
  let risingShown = $state(false);
  let fullShown = $state(false);
  let lastLabel = "";
  let wasRising = false;
  let riseTimer: ReturnType<typeof setTimeout> | undefined;
  let capTimer: ReturnType<typeof setTimeout> | undefined;
  let capped = false;
  let fullTimer: ReturnType<typeof setTimeout> | undefined;
  $effect(() => {
    const r = rising;
    const l = label;
    if (r) {
      if (!capped && !risingShown && riseTimer === undefined)
        riseTimer = setTimeout(() => {
          riseTimer = undefined;
          risingShown = true;
          wasRising = true;
          fullShown = false;
          capTimer = setTimeout(() => {
            capTimer = undefined;
            capped = true;
            risingShown = false;
          }, RISE_MAX_MS);
        }, 800);
      return;
    }
    clearTimeout(riseTimer);
    riseTimer = undefined;
    clearTimeout(capTimer);
    capTimer = undefined;
    capped = false;
    risingShown = false;
    const changed = lastLabel !== "" && l !== "" && l !== lastLabel;
    if (l) lastLabel = l;
    if (wasRising || changed) {
      wasRising = false;
      fullShown = true;
      clearTimeout(fullTimer);
      fullTimer = setTimeout(() => (fullShown = false), 4000);
    }
  });
  $effect(() => () => {
    clearTimeout(riseTimer);
    clearTimeout(capTimer);
    clearTimeout(fullTimer);
  });
</script>

<div
  class="tile"
  class:big
  role="button"
  tabindex="0"
  aria-label={self ? t("stream.preview") : t("stream.of", { name })}
  onclick={() => onselect?.()}
  ondblclick={() => onfullscreen?.()}
  onkeydown={(e) => {
    if (e.key === "Enter" || e.key === " ") {
      e.preventDefault();
      onselect?.();
    }
  }}
>
  <video bind:this={el} autoplay playsinline muted onresize={measure}></video>
  {#if !track}
    <div class="wait"><Spinner size={18} /><span>{t("stream.loading")}</span></div>
  {/if}
  {#if big}
    <span class="q" class:on={!!info && (risingShown || fullShown)} role="status">
      {#if risingShown && info}
        <Spinner size={12} />{t("stream.qualityRising")}
      {:else if fullShown && info}
        <Check size={13} strokeWidth={2.5} />{t("stream.qualityFull", { q: label })}
      {/if}
    </span>
  {/if}
  <span class="tag">
    <span class="live">LIVE</span>
    {self ? t("stream.preview") : name}
  </span>
</div>

<style>
  .tile {
    position: relative;
    min-width: 0;
    min-height: 0;
    border-radius: var(--r-md);
    overflow: hidden;
    background: #000;
    outline: none;
  }
  .tile:focus-visible {
    box-shadow: 0 0 0 2px var(--accent);
  }
  .tile.big {
    border-radius: 0;
  }
  video {
    width: 100%;
    height: 100%;
    object-fit: contain;
    display: block;
  }
  .wait {
    position: absolute;
    inset: 0;
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    gap: 8px;
    color: rgb(255 255 255 / 0.7);
    font-size: var(--fs-callout);
  }
  .tag {
    position: absolute;
    left: 8px;
    bottom: 8px;
    display: inline-flex;
    align-items: center;
    gap: 6px;
    padding: 3px 8px;
    border-radius: var(--r-sm);
    background: rgb(0 0 0 / 0.55);
    color: #fff;
    font-size: var(--fs-caption);
    font-weight: 500;
    pointer-events: none;
  }
  .q {
    position: absolute;
    right: 10px;
    top: 10px;
    display: inline-flex;
    align-items: center;
    gap: 6px;
    padding: 4px 10px;
    border-radius: 999px;
    background: rgb(0 0 0 / 0.6);
    color: #fff;
    font-size: var(--fs-caption);
    font-weight: 500;
    pointer-events: none;
    opacity: 0;
    transition: opacity var(--dur-fast);
  }
  .q.on {
    opacity: 1;
  }
  .live {
    padding: 0 4px;
    border-radius: var(--r-xs);
    background: var(--live);
    font-size: 9px;
    font-weight: 700;
    letter-spacing: 0.04em;
  }
</style>
