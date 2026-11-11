<script lang="ts">
  // Small emoji picker: common emoji rendered with the system font
  // (Segoe UI Emoji), no image pack.
  import { onMount } from "svelte";
  import { t } from "../../lib/i18n/index.svelte";

  let { onpick, onclose }: { onpick: (e: string) => void; onclose: () => void } = $props();

  const EMOJI =
    "👍 👎 ❤️ 😂 😮 😢 😡 🎉 🔥 👀 🙏 👏 💯 ✅ ❌ 🤔 😅 😎 🥳 😴 🤯 🙈 💀 😭 🥲 😁 😇 🤝 💪 🫡 🍕 🍺 ☕ 🎮 🏆 ⭐ 💡 ⚡ 🚀 🐐"
      .split(" ")
      .filter(Boolean);

  let el = $state<HTMLDivElement>();
  onMount(() => {
    el?.querySelector("button")?.focus();
    const down = (e: MouseEvent) => {
      if (el && !el.contains(e.target as Node)) onclose();
    };
    window.addEventListener("mousedown", down);
    return () => window.removeEventListener("mousedown", down);
  });

  function key(e: KeyboardEvent) {
    if (e.key === "Escape") {
      e.stopPropagation();
      onclose();
      return;
    }
    const btns = [...(el?.querySelectorAll("button") ?? [])];
    const i = btns.indexOf(document.activeElement as HTMLButtonElement);
    const step = { ArrowRight: 1, ArrowLeft: -1, ArrowDown: 8, ArrowUp: -8 }[e.key];
    if (step !== undefined && i >= 0) {
      e.preventDefault();
      btns[Math.max(0, Math.min(btns.length - 1, i + step))]?.focus();
    }
  }
</script>

<div class="picker" bind:this={el} role="dialog" aria-label={t("chat.react")} tabindex="-1" onkeydown={key}>
  {#each EMOJI as e (e)}
    <button
      type="button"
      class="emoji"
      aria-label={e}
      onclick={() => {
        onpick(e);
        onclose();
      }}>{e}</button
    >
  {/each}
</div>

<style>
  .picker {
    display: grid;
    grid-template-columns: repeat(8, 32px);
    gap: 2px;
    padding: 6px;
    border-radius: var(--r-lg);
    background: var(--bg-elevated);
    box-shadow: var(--shadow-pop);
    animation: pop var(--dur-base) var(--ease-spring);
  }
  @keyframes pop {
    from {
      opacity: 0;
      scale: 0.96;
    }
  }
  button {
    width: 32px;
    height: 32px;
    border: none;
    border-radius: var(--r-sm);
    background: transparent;
    font-size: 19px;
    line-height: 1;
  }
  button:hover,
  button:focus-visible {
    background: var(--bg-hover);
  }
</style>
