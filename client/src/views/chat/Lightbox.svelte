<script lang="ts">
  // Full-size image view; the original is only loaded here.
  import X from "@lucide/svelte/icons/x";
  import Download from "@lucide/svelte/icons/download";
  import { onMount } from "svelte";
  import { t } from "../../lib/i18n/index.svelte";

  let { src, name, onclose }: { src: string; name: string; onclose: () => void } = $props();
  let dlg = $state<HTMLDialogElement>();
  onMount(() => dlg?.showModal());
</script>

<dialog
  bind:this={dlg}
  class="lb"
  aria-label={name}
  {onclose}
  onmousedown={(e) => {
    if (e.target === dlg || (e.target as HTMLElement).classList.contains("stage")) dlg?.close();
  }}
>
  <div class="stage">
    <img {src} alt={name} decoding="async" />
  </div>
  <div class="bar">
    <span class="name">{name}</span>
    <a href={src} download={name} aria-label={t("chat.download")}><Download size={18} strokeWidth={1.75} /></a
    >
    <button type="button" aria-label={t("common.close")} onclick={() => dlg?.close()}
      ><X size={18} strokeWidth={1.75} /></button
    >
  </div>
</dialog>

<style>
  .lb {
    width: 100vw;
    height: 100vh;
    max-width: none;
    max-height: none;
    margin: 0;
    padding: 0;
    border: none;
    background: transparent;
  }
  .lb::backdrop {
    background: rgba(0, 0, 0, 0.82);
  }
  .stage {
    position: absolute;
    inset: 48px 24px 24px;
    display: flex;
    align-items: center;
    justify-content: center;
  }
  img {
    max-width: 100%;
    max-height: 100%;
    object-fit: contain;
    border-radius: var(--r-md);
    animation: in var(--dur-base) var(--ease-out);
  }
  @keyframes in {
    from {
      opacity: 0;
      transform: scale(0.98);
    }
  }
  .bar {
    position: absolute;
    top: 8px;
    left: 16px;
    right: 16px;
    display: flex;
    align-items: center;
    gap: 8px;
    color: #fff;
  }
  .name {
    flex: 1;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .bar a,
  .bar button {
    width: 32px;
    height: 32px;
    display: inline-flex;
    align-items: center;
    justify-content: center;
    border: none;
    border-radius: var(--r-sm);
    background: rgba(255, 255, 255, 0.12);
    color: #fff;
  }
</style>
