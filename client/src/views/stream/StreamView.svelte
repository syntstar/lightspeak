<script lang="ts">
  // Stream view: replaces the content area; one stream
  // large or several in a grid; theater mode, window fullscreen (F11 /
  // double click), separate stream volume, viewers of our own stream.
  import X from "@lucide/svelte/icons/x";
  import Maximize from "@lucide/svelte/icons/maximize";
  import Minimize from "@lucide/svelte/icons/minimize";
  import RectangleHorizontal from "@lucide/svelte/icons/rectangle-horizontal";
  import LayoutGrid from "@lucide/svelte/icons/layout-grid";
  import Volume2 from "@lucide/svelte/icons/volume-2";
  import VolumeX from "@lucide/svelte/icons/volume-x";
  import Eye from "@lucide/svelte/icons/eye";
  import { app } from "../../lib/state/app.svelte";
  import { t, tp } from "../../lib/i18n/index.svelte";
  import { voice } from "../../lib/voice/voice.svelte";
  import IconButton from "../../lib/ui/IconButton.svelte";
  import StreamTile from "./StreamTile.svelte";

  const stream = voice.stream;
  const me = $derived(app.me?.id);
  const big = $derived(stream.focus && stream.watching.includes(stream.focus) ? stream.focus : null);
  const others = $derived(stream.watching.filter((id) => id !== big));
  const cols = $derived(Math.ceil(Math.sqrt(stream.watching.length)));
  const name = (id: string) => app.user(id)?.displayName ?? t("common.unknownUser");
  const current = $derived(big ?? (stream.watching.length === 1 ? stream.watching[0] : null));
  const viewers = $derived(stream.viewers);
  let lastVolume = 100;

  function toggleMute(id: string) {
    const v = stream.streamVolume(id);
    if (v > 0) {
      lastVolume = v;
      stream.setStreamVolume(id, 0);
    } else stream.setStreamVolume(id, lastVolume || 100);
  }

  function onKey(e: KeyboardEvent) {
    if (e.key === "F11") {
      e.preventDefault();
      void stream.setFullscreen(!stream.fullscreen);
    } else if (e.key === "Escape" && (stream.fullscreen || stream.theater)) {
      e.preventDefault();
      void stream.setTheater(false);
    }
  }
</script>

<svelte:window onkeydown={onKey} />

<section class="sv" class:theater={stream.theater} aria-label={t("stream.view")}>
  <header class="bar">
    <span class="ttl">
      {#if current}
        {current === me ? t("stream.preview") : t("stream.of", { name: name(current) })}
      {:else}
        {tp("stream.count", stream.watching.length)}
      {/if}
    </span>
    {#if current === me && viewers.length}
      <span class="viewers" title={viewers.map(name).join(", ")}>
        <Eye size={14} strokeWidth={2} />{viewers.length}
      </span>
    {/if}
    <span class="spacer"></span>
    {#if current && current !== me}
      {@const vol = stream.streamVolume(current)}
      <IconButton label={vol ? t("stream.mute") : t("stream.unmute")} onclick={() => toggleMute(current)}>
        {#if vol}<Volume2 size={16} strokeWidth={1.75} />{:else}<VolumeX size={16} strokeWidth={1.75} />{/if}
      </IconButton>
      <input
        class="vol"
        type="range"
        min="0"
        max="200"
        step="5"
        value={vol}
        aria-label={t("stream.volume")}
        title={`${vol} %`}
        oninput={(e) => stream.setStreamVolume(current, Number(e.currentTarget.value))}
        ondblclick={() => stream.setStreamVolume(current, 100)}
      />
    {/if}
    {#if big && stream.watching.length > 1}
      <IconButton label={t("stream.grid")} onclick={() => (stream.focus = null)}>
        <LayoutGrid size={16} strokeWidth={1.75} />
      </IconButton>
    {/if}
    <IconButton
      label={t("stream.theater")}
      active={stream.theater}
      onclick={() => void stream.setTheater(!stream.theater)}
    >
      <RectangleHorizontal size={16} strokeWidth={1.75} />
    </IconButton>
    <IconButton
      label={`${stream.fullscreen ? t("stream.exitFullscreen") : t("stream.fullscreen")} (F11)`}
      onclick={() => void stream.setFullscreen(!stream.fullscreen)}
    >
      {#if stream.fullscreen}<Minimize size={16} strokeWidth={1.75} />{:else}<Maximize
          size={16}
          strokeWidth={1.75}
        />{/if}
    </IconButton>
    {#if current}
      <IconButton label={t("stream.stopWatching")} onclick={() => stream.unwatch(current)}>
        <X size={16} strokeWidth={1.75} />
      </IconButton>
    {:else}
      <IconButton label={t("stream.close")} onclick={() => stream.close()}>
        <X size={16} strokeWidth={1.75} />
      </IconButton>
    {/if}
  </header>

  {#if big}
    <div class="focus">
      <StreamTile
        userId={big}
        track={stream.tiles[big]}
        big
        onfullscreen={() => void stream.setFullscreen(!stream.fullscreen)}
      />
    </div>
    {#if others.length}
      <div class="strip">
        {#each others as id (id)}
          <StreamTile userId={id} track={stream.tiles[id]} onselect={() => (stream.focus = id)} />
        {/each}
      </div>
    {/if}
  {:else}
    <div class="grid" style:--cols={cols}>
      {#each stream.watching as id (id)}
        <StreamTile
          userId={id}
          track={stream.tiles[id]}
          onselect={() => (stream.focus = id)}
          onfullscreen={() => {
            stream.focus = id;
            void stream.setFullscreen(true);
          }}
        />
      {/each}
    </div>
  {/if}
</section>

<style>
  .sv {
    position: relative;
    display: flex;
    flex-direction: column;
    height: 100%;
    min-height: 0;
    background: #0b0b0d;
    color: #fff;
  }
  .bar {
    display: flex;
    align-items: center;
    gap: 6px;
    height: 40px;
    padding: 0 8px 0 14px;
    flex: none;
    background: rgb(255 255 255 / 0.04);
    --text-2: rgb(255 255 255 / 0.75);
    --text-1: #fff;
    --bg-hover: rgb(255 255 255 / 0.1);
  }
  .theater .bar {
    position: absolute;
    inset: 0 0 auto 0;
    z-index: 2;
    background: linear-gradient(rgb(0 0 0 / 0.6), transparent);
    opacity: 0;
    transition: opacity var(--dur-fast);
  }
  .theater:hover .bar,
  .theater .bar:focus-within {
    opacity: 1;
  }
  .ttl {
    font-weight: 600;
    font-size: var(--fs-callout);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .viewers {
    display: inline-flex;
    align-items: center;
    gap: 4px;
    color: rgb(255 255 255 / 0.7);
    font-size: var(--fs-caption);
  }
  .spacer {
    flex: 1;
  }
  .vol {
    width: 96px;
    accent-color: var(--accent);
  }
  .focus {
    flex: 1;
    min-height: 0;
    display: flex;
  }
  .focus :global(.tile) {
    flex: 1;
  }
  .strip {
    flex: none;
    display: flex;
    gap: 8px;
    height: 110px;
    padding: 8px;
  }
  .strip :global(.tile) {
    width: 196px;
    flex: none;
    cursor: pointer;
  }
  .grid {
    flex: 1;
    min-height: 0;
    display: grid;
    grid-template-columns: repeat(var(--cols), 1fr);
    align-content: center;
    gap: 8px;
    padding: 8px;
    overflow: auto;
  }
  .grid :global(.tile) {
    aspect-ratio: 16 / 9;
    cursor: pointer;
  }
</style>
