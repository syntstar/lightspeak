<script lang="ts">
  // Records a global key combination via the native hook (works for mouse
  // buttons 4/5 and while other apps have focus). Escape cancels.
  import X from "@lucide/svelte/icons/x";
  import { onDestroy } from "svelte";
  import { t } from "../../lib/i18n/index.svelte";
  import { native, hasStore } from "../../lib/native";
  import { voice } from "../../lib/voice/voice.svelte";
  import { toast } from "../../lib/ui/toast.svelte";

  let {
    keys = $bindable(),
    label,
    onchange,
  }: { keys: number[]; label: string; onchange?: () => void } = $props();

  let recording = $state(false);
  let names = $state<Record<number, string>>({});
  let un: (() => void) | null = null;

  $effect(() => {
    for (const k of keys) {
      if (names[k] === undefined && hasStore) {
        void native.keyName(k).then((n) => (names[k] = n ?? `#${k}`));
      }
    }
  });

  const text = $derived(keys.length ? keys.map((k) => names[k] ?? "…").join(" + ") : t("keys.none"));

  async function start() {
    if (!hasStore || recording) return;
    recording = true;
    un ??= await native.onInput((ev) => {
      if (ev.kind !== "recorded" || !recording) return;
      keys = ev.keys;
      void finish(true);
    });
    try {
      await native.inputRecord();
    } catch {
      toast(t("voice.hookFailed"), "error");
      await finish(false);
    }
  }

  async function finish(changed: boolean) {
    recording = false;
    un?.();
    un = null;
    await native.inputStop().catch(() => {});
    await voice.restartInput();
    if (changed) onchange?.();
  }

  function clear() {
    keys = [];
    onchange?.();
    void voice.restartInput();
  }

  onDestroy(() => {
    if (recording) void finish(false);
  });
</script>

<span class="kr">
  <button
    type="button"
    class="rec"
    class:recording
    aria-label={`${label}: ${recording ? t("keys.press") : text}`}
    onclick={() => void start()}
    onkeydown={(e) => {
      if (recording && e.key === "Escape") {
        e.preventDefault();
        e.stopPropagation();
        void finish(false);
      }
    }}
  >
    {recording ? t("keys.press") : text}
  </button>
  {#if keys.length && !recording}
    <button type="button" class="clr" aria-label={t("keys.clear")} title={t("keys.clear")} onclick={clear}>
      <X size={12} strokeWidth={2.25} />
    </button>
  {/if}
</span>

<style>
  .kr {
    display: inline-flex;
    align-items: center;
    gap: 4px;
  }
  .rec {
    min-width: 120px;
    height: 26px;
    padding: 0 10px;
    border: 1px solid var(--separator);
    border-radius: var(--r-sm);
    background: var(--bg-active);
    font-family: var(--font-mono, inherit);
    font-size: var(--fs-callout);
  }
  .rec.recording {
    border-color: var(--accent);
    box-shadow: 0 0 0 3px color-mix(in srgb, var(--accent) 35%, transparent);
    color: var(--accent-text);
  }
  .clr {
    display: inline-flex;
    padding: 4px;
    border: none;
    border-radius: 50%;
    background: transparent;
    color: var(--text-2);
  }
  .clr:hover {
    background: var(--bg-hover);
  }
</style>
