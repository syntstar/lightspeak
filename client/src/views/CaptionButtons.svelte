<script lang="ts">
  // Windows caption buttons (Segoe Fluent Icons glyphs).
  import { getCurrentWindow } from "@tauri-apps/api/window";
  import { onMount } from "svelte";
  import { t } from "../lib/i18n/index.svelte";
  import { inTauri } from "../lib/native";

  let maximized = $state(false);

  onMount(() => {
    if (!inTauri) return;
    const w = getCurrentWindow();
    const sync = () =>
      void w
        .isMaximized()
        .then((m) => (maximized = m))
        .catch(() => {});
    sync();
    const un = w.onResized(sync);
    return () => void un.then((f) => f());
  });

  const win = () => getCurrentWindow();
  const GLYPH_RESTORE = String.fromCharCode(0xe923);
  const GLYPH_MAXIMIZE = String.fromCharCode(0xe922);
</script>

{#if inTauri}
  <div class="caps">
    <button
      type="button"
      aria-label={t("window.minimize")}
      tabindex="-1"
      onclick={() => void win().minimize()}>&#xE921;</button
    >
    <button
      type="button"
      aria-label={maximized ? t("window.restore") : t("window.maximize")}
      tabindex="-1"
      onclick={() => void win().toggleMaximize()}>{maximized ? GLYPH_RESTORE : GLYPH_MAXIMIZE}</button
    >
    <button
      type="button"
      class="close"
      aria-label={t("window.close")}
      tabindex="-1"
      onclick={() => void win().close()}>&#xE8BB;</button
    >
  </div>
{/if}

<style>
  .caps {
    display: flex;
    height: var(--titlebar-h);
    flex: none;
  }
  button {
    width: 46px;
    height: 100%;
    border: none;
    background: transparent;
    color: var(--text-1);
    font-family: var(--font-caption-icons);
    font-size: 10px;
    display: inline-flex;
    align-items: center;
    justify-content: center;
  }
  button:hover {
    background: var(--bg-hover);
  }
  button:active {
    background: var(--bg-active);
  }
  .close:hover {
    background: var(--caption-close);
    color: #fff;
  }
  .close:active {
    background: color-mix(in srgb, var(--caption-close) 80%, black);
    color: #fff;
  }
</style>
